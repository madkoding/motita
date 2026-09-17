// Package sandbox implementa la Capa C: ejecución aislada ligera, sin Docker.
//
// Aislamiento aplicado, en capas, según lo que el sistema permita:
//
//  1. Directorio efímero por intento (siempre). Se borra al terminar salvo que
//     se pida conservarlo.
//  2. setrlimit en el hijo: CPU, memoria (espacio de direcciones), procesos,
//     descriptores y tamaño máximo de archivo. No requiere privilegios.
//  3. cgroups v1 para memoria y PIDs, si existen y hay permiso de escritura. Un
//     setrlimit de memoria es una aproximación (RLIMIT_AS acota el espacio de
//     direcciones, no el residente); cgroups da la cota real.
//  4. chroot + bajada de privilegios, sólo si el proceso es root y se pidió.
//  5. Espacio de nombres de red propio (CLONE_NEWNET) si se pide aislar la red.
//
// Los setrlimit y el chroot los aplica un proceso hijo que es el propio binario
// re-ejecutado (ver hijo.go): así no se depende de util-linux ni de cgo, y no
// queda ningún proceso Go intermedio consumiendo el presupuesto limitado.
//
// Todo lo que NO se pudo aplicar queda registrado como aviso: un sandbox que no
// aísla pero dice aislar es peor que no tener sandbox.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// Modo de aislamiento efectivamente aplicado.
type Modo string

const (
	ModoEfimero Modo = "efimero"
	ModoLimites Modo = "limites_posix"
	ModoCgroups Modo = "cgroups_v1"
	ModoChroot  Modo = "chroot"
	ModoSinRed  Modo = "netns_sin_red"
	ModoSinPriv Modo = "sin_privilegios"
)

// Opciones de construcción del sandbox.
type Opciones struct {
	Dir         string // directorio base de trabajo
	Limites     Limites
	UsarCgroups bool
	CgroupRaiz  string
	UsarChroot  bool
	Raiz        string
	Uid, Gid    int
	DropPrivs   bool
	Conservar   bool
	Timeout     time.Duration
	MaxSalidaKB int
	Log         *logx.Logger
}

// Sandbox ejecuta comandos en un entorno controlado.
type Sandbox struct {
	op         Opciones
	log        *logx.Logger
	cg         *cgroup
	base       string
	ejecutable string
	sinAplicar []string
}

// Nuevo prepara el sandbox y detecta qué aislamiento está disponible de verdad.
func Nuevo(op Opciones) (*Sandbox, error) {
	if op.Log == nil {
		op.Log = logx.Global()
	}
	if op.Dir == "" {
		op.Dir = "."
	}
	if err := os.MkdirAll(op.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("no se pudo crear el directorio de trabajo %q: %w", op.Dir, err)
	}
	abs, err := filepath.Abs(op.Dir)
	if err != nil {
		return nil, fmt.Errorf("no se pudo resolver el directorio de trabajo %q: %w", op.Dir, err)
	}
	autoEjecutable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("no se pudo localizar el propio ejecutable (necesario para el aislamiento): %w", err)
	}

	s := &Sandbox{op: op, log: op.Log, base: abs, ejecutable: autoEjecutable}

	if op.UsarChroot {
		switch {
		case os.Geteuid() != 0:
			s.sinAplicar = append(s.sinAplicar, "chroot: se pidió pero el proceso no es root")
			s.op.UsarChroot = false
		default:
			if _, err := os.Stat(op.Raiz); err != nil {
				s.sinAplicar = append(s.sinAplicar, fmt.Sprintf("chroot: la raíz %q no es accesible: %v", op.Raiz, err))
				s.op.UsarChroot = false
			}
		}
	}

	if op.UsarCgroups {
		cg, err := nuevoCgroup(op.CgroupRaiz, op.Limites)
		if err != nil {
			s.sinAplicar = append(s.sinAplicar, "cgroups v1: "+err.Error())
			s.log.Warn("cgroups v1 no disponibles: se aplican sólo límites POSIX", "error", err)
		} else {
			s.cg = cg
		}
	}

	// El proceso principal entra al cgroup para que sus hijos hereden la
	// pertenencia aunque el re-exec no lo haga explícitamente.
	if s.cg != nil {
		if err := s.cg.agregarProceso(os.Getpid()); err != nil {
			s.sinAplicar = append(s.sinAplicar, "cgroups v1: no se pudo agregar el proceso al grupo: "+err.Error())
			s.log.Warn("no se pudo agregar el proceso al cgroup", "error", err)
		}
	}

	if !hayLimites(op.Limites) {
		s.log.Debug("sin límites POSIX configurados: sólo se aísla el directorio efímero")
	}

	// Un RLIMIT_AS por debajo del espacio de direcciones que el proceso de
	// aislamiento ya usa haría abortar al propio runtime de Go (fatal error:
	// runtime: cannot allocate memory). Se ajusta aquí, en el padre, y se
	// registra: el hijo no puede avisar sin contaminar la salida del comando.
	if s.op.Limites.MemoriaMB > 0 {
		if minima := minimaMemoriaNecesaria(); minima > 0 && s.op.Limites.MemoriaMB < minima {
			s.sinAplicar = append(s.sinAplicar, fmt.Sprintf(
				"memoria_mb: se aplica %d MB en lugar de %d porque el proceso que lanza el sandbox ya usa ese espacio de direcciones",
				minima, s.op.Limites.MemoriaMB))
			s.log.Warn("se eleva el límite de memoria del sandbox",
				"solicitado_mb", s.op.Limites.MemoriaMB, "aplicado_mb", minima)
			s.op.Limites.MemoriaMB = minima
		}
	}

	s.log.Info("sandbox preparado",
		"directorio", s.base,
		"chroot", s.op.UsarChroot,
		"cgroups", s.cg != nil,
		"limites", hayLimites(op.Limites),
		"sin_aplicar", strings.Join(s.sinAplicar, " | "))
	return s, nil
}

// Cerrar libera el grupo de cgroups si se creó.
func (s *Sandbox) Cerrar() error {
	if s.cg == nil {
		return nil
	}
	if err := s.cg.eliminar(); err != nil {
		s.log.Warn("no se pudo eliminar el cgroup", "error", err)
		return err
	}
	return nil
}

// Aislamiento devuelve una descripción de lo que realmente se aplica.
func (s *Sandbox) Aislamiento() []Modo {
	modos := []Modo{ModoEfimero}
	if hayLimites(s.op.Limites) {
		modos = append(modos, ModoLimites)
	}
	if s.cg != nil {
		modos = append(modos, ModoCgroups)
	}
	if s.op.UsarChroot {
		modos = append(modos, ModoChroot)
	}
	if s.op.DropPrivs {
		modos = append(modos, ModoSinPriv)
	}
	if s.op.Limites.SinRed {
		modos = append(modos, ModoSinRed)
	}
	return modos
}

// SinAplicar lista el aislamiento que se pidió y no se pudo aplicar.
func (s *Sandbox) SinAplicar() []string { return s.sinAplicar }

// Ejecutar corre un comando aislado. Devuelve salida combinada, si se truncó, el
// código de salida y un error sólo cuando el comando no pudo ejecutarse.
func (s *Sandbox) Ejecutar(ctx context.Context, p execx.Peticion) (string, bool, int, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = s.op.Timeout
	}
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	max := p.MaxSalida
	if max <= 0 {
		max = int64(s.op.MaxSalidaKB) << 10
	}
	if max <= 0 {
		max = 256 << 10
	}

	// Directorio de trabajo: el que pida quien llama, o el base.
	//
	// Importante: NO se usa un directorio efímero como directorio de trabajo. El
	// efecto del trabajo (el archivo generado, el commit, el informe) tiene que
	// ser visible después para el ancla y para la acción final; si las acciones
	// corrieran en un directorio que se borra al terminar, el validador no
	// podría ver nunca el resultado y el agente fallaría siempre. El aislamiento
	// efímero se aplica a TMPDIR, que es donde van los archivos temporales.
	dirTrabajo := p.Dir
	if dirTrabajo == "" {
		dirTrabajo = s.base
	}
	if err := os.MkdirAll(dirTrabajo, 0o755); err != nil {
		return "", false, -1, fmt.Errorf("no se pudo preparar el directorio de trabajo %q: %w", dirTrabajo, err)
	}

	// TMPDIR efímero y propio de esta ejecución.
	dirTemporal, err := os.MkdirTemp(s.base, "tmp-*")
	if err != nil {
		return "", false, -1, fmt.Errorf("no se pudo crear el directorio temporal: %w", err)
	}
	if !s.op.Conservar {
		defer func() {
			if err := os.RemoveAll(dirTemporal); err != nil {
				s.log.Warn("no se pudo borrar el directorio temporal", "dir", dirTemporal, "error", err)
			}
		}()
	}

	espec := Espec{
		Comando: p.Comando,
		Args:    append([]string(nil), p.Args...),
		Dir:     dirTrabajo,
		Limites: s.op.Limites,
		Entorno: s.entornoConTmp(dirTemporal),
	}

	comando := s.ejecutable
	var args []string

	if s.op.UsarChroot {
		// Dentro del chroot el comando real es la ruta relativa a la raíz.
		espec.Chroot = s.op.Raiz
		espec.DirChroot = "/" + strings.TrimPrefix(strings.TrimPrefix(dirTrabajo, s.base), "/")
		espec.DropPrivs = s.op.DropPrivs
		espec.Uid, espec.Gid = s.op.Uid, s.op.Gid

		dentro := filepath.Join(espec.Chroot, p.Comando)
		if _, err := os.Stat(dentro); err != nil {
			return "", false, -1, fmt.Errorf("el comando %q no existe dentro del chroot %q: %w", p.Comando, espec.Chroot, err)
		}
	} else {
		espec.DropPrivs = s.op.DropPrivs
		espec.Uid, espec.Gid = s.op.Uid, s.op.Gid
	}

	codificada, err := espec.Codificar()
	if err != nil {
		return "", false, -1, err
	}
	args = []string{MarcaHijo, codificada}

	s.log.Debug("ejecutando en sandbox",
		"comando", p.Comando,
		"dir_trabajo", dirTrabajo,
		"dir_temporal", dirTemporal,
		"aislamiento", strings.Join(modosATexto(s.Aislamiento()), ","))

	return s.lanzar(ctx, comando, args, dirTrabajo, timeout, max)
}

// lanzar arranca el hijo de aislamiento y recoge su salida.
func (s *Sandbox) lanzar(ctx context.Context, comando string, args []string, dir string, timeout time.Duration, maxSalida int64) (string, bool, int, error) {
	ctxHijo, cancelar := context.WithTimeout(ctx, timeout)
	defer cancelar()

	cmd := exec.CommandContext(ctxHijo, comando, args...)
	cmd.Dir = dir
	cmd.Env = s.entorno()

	attr, avisos := atributosHijo(s.op.Limites, false, 0, 0)
	if attr != nil {
		cmd.SysProcAttr = attr
	}
	for _, aviso := range avisos {
		s.sinAplicar = append(s.sinAplicar, aviso)
	}
	cmd.Cancel = func() error { return matarGrupo(cmd) }
	cmd.WaitDelay = 2 * time.Second

	salida := &bufferLimitado{max: maxSalida}
	cmd.Stdout = salida
	cmd.Stderr = salida

	inicio := time.Now()
	err := cmd.Run()
	duracion := time.Since(inicio)
	texto := salida.buf.String()

	if ctxHijo.Err() == context.DeadlineExceeded {
		return texto, salida.truncado, -1, fmt.Errorf("el comando excedió el límite de %s en el sandbox", timeout)
	}

	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
			if exit == 127 {
				return texto, salida.truncado, exit, fmt.Errorf("el comando no pudo ejecutarse dentro del sandbox (código 127)")
			}
			if exit < 0 {
				return texto, salida.truncado, exit, fmt.Errorf("el proceso fue terminado por una señal (exit=%d): revisa los límites de memoria y CPU del sandbox", exit)
			}
			return texto, salida.truncado, exit, nil
		}
		return texto, salida.truncado, -1, fmt.Errorf("no se pudo arrancar el aislamiento: %w", err)
	}
	s.log.Debug("ejecución del sandbox terminada", "exit", exit, "duracion_ms", duracion.Milliseconds(), "truncado", salida.truncado)
	return texto, salida.truncado, exit, nil
}

// entornoConTmp devuelve el entorno del hijo: acotado, sin secretos del agente
// y con TMPDIR apuntando al directorio temporal efímero de esta ejecución.
func (s *Sandbox) entornoConTmp(dirTemporal string) []string {
	base := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + s.base,
		"LANG=C.UTF-8",
		"TMPDIR=" + dirTemporal,
	}
	for _, clave := range []string{"TERM", "USER", "SHELL"} {
		if v, ok := os.LookupEnv(clave); ok {
			base = append(base, clave+"="+v)
		}
	}
	return base
}

// entorno es el entorno sin directorio temporal propio (para el proceso de
// aislamiento, que no necesita TMPDIR).
func (s *Sandbox) entorno() []string {
	return s.entornoConTmp(s.base)
}

// bufferLimitado acumula hasta max bytes y marca si hubo recorte.
type bufferLimitado struct {
	buf      strings.Builder
	max      int64
	truncado bool
}

func (b *bufferLimitado) Write(p []byte) (int, error) {
	espacio := b.max - int64(b.buf.Len())
	if espacio <= 0 {
		b.truncado = true
		return len(p), nil
	}
	if espacio < int64(len(p)) {
		b.buf.Write(p[:espacio])
		b.truncado = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func modosATexto(modos []Modo) []string {
	salida := make([]string, 0, len(modos))
	for _, m := range modos {
		salida = append(salida, string(m))
	}
	return salida
}

// JSONAislamiento expone el aislamiento aplicado para los registros.
func (s *Sandbox) JSONAislamiento() string {
	datos, err := json.Marshal(map[string]any{
		"modos":       modosATexto(s.Aislamiento()),
		"sin_aplicar": s.sinAplicar,
		"directorio":  s.base,
	})
	if err != nil {
		return `{"modos":[],"sin_aplicar":["no serializable"]}`
	}
	return string(datos)
}
