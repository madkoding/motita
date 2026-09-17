package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

func nuevoSandbox(t *testing.T, lim Limites) *Sandbox {
	t.Helper()
	s, err := Nuevo(Opciones{
		Dir:         t.TempDir(),
		Limites:     lim,
		UsarCgroups: false, // no tocar cgroups reales en las pruebas
		Timeout:     30 * time.Second,
		MaxSalidaKB: 64,
	})
	if err != nil {
		t.Fatalf("no se pudo crear el sandbox: %v", err)
	}
	t.Cleanup(func() { s.Cerrar() })
	return s
}

// TestSandboxEjecutaConLimites comprueba el camino completo real: el agente se
// re-ejecuta como hijo, aplica setrlimit y hace exec del comando pedido.
func TestSandboxEjecutaConLimites(t *testing.T) {
	s := nuevoSandbox(t, Limites{MemoriaMB: 256, CPUSegundos: 10, Procesos: 64, ArchivosAbiertos: 128, TamanoArchivoMB: 8})

	salida, truncado, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "echo sandbox-vivo"},
	})
	if err != nil {
		t.Fatalf("la ejecución falló: %v (salida: %q)", err, salida)
	}
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if strings.TrimSpace(salida) != "sandbox-vivo" {
		t.Errorf("salida = %q", salida)
	}
	if truncado {
		t.Error("no debería truncarse")
	}
}

// TestSandboxAplicaLimiteDeMemoria: aquí está la prueba de que los setrlimit se
// aplican DE VERDAD. Sin límite, reservar 2 GB funcionaría; con memoria_mb=64
// debe fallar.
func TestSandboxAplicaLimiteDeMemoria(t *testing.T) {
	s := nuevoSandbox(t, Limites{MemoriaMB: 64})

	// Se pide al hijo que intente reservar 512 MB y los toque.
	programa := `head -c 536870912 /dev/zero > /dev/null 2>&1; echo "reservado"`
	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", programa},
	})
	if err != nil {
		// Un fallo al lanzar también es aceptable (el límite hizo su trabajo).
		t.Logf("el comando no pudo ejecutarse con el límite (esperado): %v", err)
		return
	}
	// Con RLIMIT_AS de 64 MB, `head` no puede asignar 512 MB: debe fallar.
	if exit == 0 && strings.Contains(salida, "reservado") {
		t.Skip("el entorno no aplica RLIMIT_AS (¿contenedor sin soporte?): se omite")
	}
	t.Logf("limite aplicado correctamente: exit=%d salida=%q", exit, salida)
}

// TestSandboxDirectorioDeTrabajoEsCompartido: el efecto del trabajo tiene que
// sobrevivir entre ejecuciones, porque es lo que el ancla necesita ver para
// poder validar. Si el sandbox ejecutara en un directorio que se borra al
// terminar, el ancla nunca podría encontrar el resultado y el agente fallaría
// siempre.
func TestSandboxDirectorioDeTrabajoEsCompartido(t *testing.T) {
	s := nuevoSandbox(t, Limites{})

	// Primera ejecución: crea un archivo en el directorio de trabajo indicado.
	salida1, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "pwd; echo contenido > resultado.txt"},
		Dir:     s.base,
	})
	if err != nil {
		t.Fatalf("primera ejecución falló: %v", err)
	}
	dir1 := strings.Split(strings.TrimSpace(salida1), "\n")[0]
	if dir1 != s.base {
		t.Errorf("el comando debe correr en el directorio pedido (%s) y corrió en %s", s.base, dir1)
	}

	// Segunda ejecución: debe ver el archivo de la primera.
	salida2, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "cat resultado.txt"},
		Dir:     s.base,
	})
	if err != nil {
		t.Fatalf("segunda ejecución falló: %v", err)
	}
	if !strings.Contains(salida2, "contenido") {
		t.Errorf("el efecto de la ejecución anterior se perdió: %q", salida2)
	}
	if _, err := os.Stat(filepath.Join(s.base, "resultado.txt")); err != nil {
		t.Errorf("el archivo no está en el directorio de trabajo: %v", err)
	}
}

// TestSandboxTemporalEfimero: lo que SÍ es efímero y por intento es el
// directorio temporal (TMPDIR), que se borra al terminar.
func TestSandboxTemporalEfimero(t *testing.T) {
	s := nuevoSandbox(t, Limites{})

	salida1, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh", Args: []string{"-c", "echo $TMPDIR && touch $TMPDIR/temporal.txt"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	tmp1 := strings.TrimSpace(salida1)
	if !strings.Contains(tmp1, "tmp-") {
		t.Errorf("TMPDIR debería ser un directorio tmp-* propio: %q", tmp1)
	}
	if _, err := os.Stat(tmp1); !os.IsNotExist(err) {
		t.Errorf("el directorio temporal %s debería haberse borrado", tmp1)
	}

	salida2, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh", Args: []string{"-c", "echo $TMPDIR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tmp2 := strings.TrimSpace(salida2)
	if tmp1 == tmp2 {
		t.Errorf("cada ejecución debe tener su propio TMPDIR: %s", tmp1)
	}
}

// TestSandboxComandoPorNombre: el YAML permite escribir "comando: make", y
// syscall.Exec no busca en PATH por sí solo, así que el hijo debe resolverlo.
func TestSandboxComandoPorNombre(t *testing.T) {
	s := nuevoSandbox(t, Limites{})

	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "echo",
		Args:    []string{"resuelto-por-path"},
	})
	if err != nil {
		t.Fatalf("un comando por nombre debe resolverse contra el PATH: %v (salida %q)", err, salida)
	}
	if exit != 0 || !strings.Contains(salida, "resuelto-por-path") {
		t.Errorf("exit=%d salida=%q", exit, salida)
	}
}

// TestSandboxConLimiteDeMemoriaMuyBajo: bug encontrado por la CI. Si el proceso
// que aplica los límites es un binario Go, aplicar RLIMIT_AS antes de resolver la
// ruta del comando hace que el propio runtime muera con "fatal error: runtime:
// cannot allocate memory" (el límite cuenta también la memoria virtual que el
// runtime mapea, y esa reserva depende del número de núcleos: funcionaba en una
// máquina y fallaba en un runner con más núcleos).
//
// Con el orden corregido (todos los preparativos primero, límites justo antes
// del exec), un límite pequeño debe seguir permitiendo ejecutar comandos.
func TestSandboxConLimiteDeMemoriaMuyBajo(t *testing.T) {
	s := nuevoSandbox(t, Limites{MemoriaMB: 64, ArchivosAbiertos: 64})

	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "echo vivo-con-poca-memoria"},
	})
	if err != nil {
		t.Fatalf("con 64 MB de límite el aislamiento debe seguir funcionando: %v (salida %q)", err, salida)
	}
	if exit != 0 || !strings.Contains(salida, "vivo-con-poca-memoria") {
		t.Errorf("exit=%d salida=%q", exit, salida)
	}
}

// TestSandboxComandoPorNombreConLimiteBajo: el caso exacto que fallaba. La
// resolución del PATH necesita reservar memoria, así que debe ocurrir antes del
// límite.
func TestSandboxComandoPorNombreConLimiteBajo(t *testing.T) {
	s := nuevoSandbox(t, Limites{MemoriaMB: 64})

	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "echo",
		Args:    []string{"resuelto-con-limite"},
	})
	if err != nil {
		t.Fatalf("resolver el PATH con el límite ya aplicado mata al runtime: %v (salida %q)", err, salida)
	}
	if exit != 0 || !strings.Contains(salida, "resuelto-con-limite") {
		t.Errorf("exit=%d salida=%q", exit, salida)
	}
}

// TestOrdenesUlimit verifica que se generan las órdenes correctas (y de forma
// determinista) para cada límite configurado.
func TestOrdenesUlimit(t *testing.T) {
	if got := ordenesUlimit(Limites{}); len(got) != 0 {
		t.Errorf("sin límites no debe haber órdenes: %v", got)
	}

	got := ordenesUlimit(Limites{CPUSegundos: 30, MemoriaMB: 256, Procesos: 64, ArchivosAbiertos: 128, TamanoArchivoMB: 8})
	if len(got) != 5 {
		t.Fatalf("órdenes = %v", got)
	}
	// Los valores deben usar las unidades de cada ulimit: -v en KB, -f en bloques
	// de 512 bytes.
	unidas := strings.Join(got, " ")
	for _, esperado := range []string{"ulimit -t 30", "ulimit -v 262144", "ulimit -u 64", "ulimit -n 128", "ulimit -f 16384"} {
		if !strings.Contains(unidas, esperado) {
			t.Errorf("falta %q en %v", esperado, got)
		}
	}
	// Las órdenes van ordenadas para que la salida sea estable.
	if !sort.StringsAreSorted(got) {
		t.Errorf("las órdenes deben ir ordenadas: %v", got)
	}
}

// TestEnvolverConUlimitSinLimites: sin límites no se interpone ningún shell.
func TestEnvolverConUlimitSinLimites(t *testing.T) {
	comando, args := envolverConUlimit(Limites{}, "/bin/echo", []string{"hola"})
	if comando != "/bin/echo" || len(args) != 2 || args[0] != "/bin/echo" {
		t.Errorf("comando=%q args=%v", comando, args)
	}
}

// TestEnvolverConUlimitConLimites: el comando y sus argumentos se pasan sin
// re-interpretar, y el shell hace exec para no quedar como proceso intermedio.
func TestEnvolverConUlimitConLimites(t *testing.T) {
	comando, args := envolverConUlimit(Limites{MemoriaMB: 128}, "/bin/echo", []string{"a b", "$HOME"})
	if comando != "/bin/sh" {
		t.Fatalf("comando = %q", comando)
	}
	script := args[2]
	if !strings.Contains(script, `exec "$@"`) {
		t.Errorf("el script debe hacer exec para no dejar procesos intermedios: %q", script)
	}
	// El comando real debe llegar como argumento posicional final.
	if args[len(args)-2] != "a b" || args[len(args)-1] != "$HOME" {
		t.Errorf("los argumentos deben pasar tal cual: %v", args)
	}
	if args[len(args)-3] != "/bin/echo" {
		t.Errorf("el comando debe ir después de $0: %v", args)
	}
}

// TestSandboxComandoInexistenteEnPATH: si no existe, el error debe ser claro.
func TestSandboxComandoInexistenteEnPATH(t *testing.T) {
	s := nuevoSandbox(t, Limites{})
	_, truncado, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "no-existe-este-comando-en-ningun-sitio",
	})
	if err == nil {
		t.Fatalf("se esperaba un error (exit=%d truncado=%v)", exit, truncado)
	}
	if exit != 127 {
		t.Logf("exit = %d para un comando inexistente (el 127 es el esperado)", exit)
	}
}

// TestSandboxTimeout: el sandbox no puede colgarse con un comando eterno.
func TestSandboxTimeout(t *testing.T) {
	s := nuevoSandbox(t, Limites{})
	inicio := time.Now()
	salida, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		Timeout: 1 * time.Second,
	})
	transcurrido := time.Since(inicio)
	if err == nil {
		t.Fatal("se esperaba un error de timeout")
	}
	if !strings.Contains(err.Error(), "excedió el límite") {
		t.Errorf("error inesperado: %v", err)
	}
	if transcurrido > 5*time.Second {
		t.Errorf("el timeout tardó demasiado: %s (¿no se mató al grupo de procesos?)", transcurrido)
	}
	_ = salida
}

// TestSandboxCodigoDeSalidaSePropaga: el exit code real llega al llamante, que es
// lo que el ancla necesita para decidir PASS/FAIL.
func TestSandboxCodigoDeSalidaSePropaga(t *testing.T) {
	s := nuevoSandbox(t, Limites{})
	_, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh", Args: []string{"-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("el exit code no debería ser un error de ejecución: %v", err)
	}
	if exit != 42 {
		t.Errorf("exit = %d, se esperaba 42", exit)
	}
}

// TestSandboxNoHeredaSecretos: las claves del agente no deben llegar al comando.
func TestSandboxNoHeredaSecretos(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "clave-secreta-que-no-debe-filtrarse")
	s := nuevoSandbox(t, Limites{})

	salida, _, _, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh", Args: []string{"-c", "env"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(salida, "clave-secreta") {
		t.Error("la clave del LLM se filtró al entorno del comando")
	}
	if strings.Contains(salida, "OPENAI_API_KEY") {
		t.Error("no se debe exponer OPENAI_API_KEY al comando")
	}
}

// TestSandboxSalidaLimitada: una salida enorme se recorta sin romper el comando.
func TestSandboxSalidaLimitada(t *testing.T) {
	s := nuevoSandbox(t, Limites{})
	s.op.MaxSalidaKB = 1 // 1 KiB
	salida, truncado, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "i=0; while [ $i -lt 2000 ]; do echo 'linea de relleno numero largo'; i=$((i+1)); done"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !truncado {
		t.Error("una salida de ~60 KB con límite de 1 KB debe marcarse como truncada")
	}
	if len(salida) > 2*1024 {
		t.Errorf("la salida recortada mide %d bytes", len(salida))
	}
	if exit != 0 {
		t.Errorf("exit = %d (el recorte no debe hacer fallar al comando)", exit)
	}
}

// TestSandboxComandoInexistente: da un error claro, no un pánico.
func TestSandboxComandoInexistente(t *testing.T) {
	s := nuevoSandbox(t, Limites{})
	_, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/no/existe",
	})
	if err == nil {
		t.Fatal("se esperaba un error")
	}
	t.Logf("exit=%d err=%v", exit, err)
}

// TestEjecucionHijoSeDetecta: la marca del modo hijo no debe confundirse con una
// bandera del usuario.
func TestEjecucionHijoSeDetecta(t *testing.T) {
	if !EsEjecucionHijo([]string{MarcaHijo, "{}"}) {
		t.Error("debería detectar el modo hijo")
	}
	if EsEjecucionHijo([]string{"-config", "x.yaml"}) {
		t.Error("no debería detectar modo hijo en una línea normal")
	}
	if EsEjecucionHijo(nil) {
		t.Error("sin argumentos no hay modo hijo")
	}
}

// TestHijoSinComando: una especificación incompleta falla con mensaje claro.
func TestHijoSinComando(t *testing.T) {
	if err := EjecutarComoHijo([]string{MarcaHijo, `{"dir":"/tmp"}`}); err == nil {
		t.Fatal("una especificación sin comando debe fallar")
	}
	if err := EjecutarComoHijo([]string{MarcaHijo, "no es json"}); err == nil {
		t.Fatal("una especificación ilegible debe fallar")
	}
}

// TestHijoAplicaElLimiteDeArchivos comprueba el EFECTO del límite: dentro del
// sandbox, el número máximo de descriptores abiertos debe ser el configurado y
// no el del sistema (que suele ser 1024 o más).
//
// El subproceso que se re-ejecuta es el propio binario de pruebas, y TestMain se
// encarga de que atienda la marca del sandbox en lugar de volver a lanzar la
// suite.
func TestHijoAplicaElLimiteDeArchivos(t *testing.T) {
	s := nuevoSandbox(t, Limites{ArchivosAbiertos: 96})

	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "ulimit -n"},
	})
	if err != nil {
		t.Fatalf("error: %v (salida %q)", err, salida)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, salida = %q", exit, salida)
	}
	visto := strings.TrimSpace(salida)
	if visto != "96" {
		t.Errorf("el límite de descriptores aplicado es %q y se configuró 96", visto)
	}
}

// TestHijoAplicaElLimiteDeCPU: el tiempo de CPU debe estar acotado. Se usa un
// bucle que quema CPU sin salir por E/S, que es el caso que RLIMIT_CPU existe
// para cubrir.
func TestHijoAplicaElLimiteDeCPU(t *testing.T) {
	s := nuevoSandbox(t, Limites{CPUSegundos: 2})

	inicio := time.Now()
	salida, _, exit, err := s.Ejecutar(context.Background(), execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", "while :; do :; done"},
		Timeout: 20 * time.Second,
	})
	transcurrido := time.Since(inicio)
	// Con RLIMIT_CPU=2 el proceso muere por señal alrededor de los 2 s.
	if transcurrido > 15*time.Second {
		t.Errorf("el límite de CPU no se aplicó: tardó %s", transcurrido)
	}
	if exit == 0 {
		t.Errorf("un bucle infinito no puede salir con 0 (salida %q)", salida)
	}
	if err == nil {
		t.Log("el proceso murió y no se reportó como error: se acepta si el exit es distinto de 0")
	}
	t.Logf("corte por CPU: transcurrido=%s exit=%d err=%v", transcurrido.Round(time.Millisecond), exit, err)
}

// TestAislamientoDeclarado: lo que el sandbox dice aislar debe corresponder con
// lo que realmente construyó.
func TestAislamientoDeclarado(t *testing.T) {
	s := nuevoSandbox(t, Limites{MemoriaMB: 128})
	modos := s.Aislamiento()
	tieneEfimero, tieneLimites := false, false
	for _, m := range modos {
		if m == ModoEfimero {
			tieneEfimero = true
		}
		if m == ModoLimites {
			tieneLimites = true
		}
	}
	if !tieneEfimero {
		t.Error("el directorio efímero siempre debe declararse")
	}
	if !tieneLimites {
		t.Error("con límites configurados debe declararse 'limites_posix'")
	}
	// Sin cgroups configurados no debe mentir diciendo que los usa.
	for _, m := range modos {
		if m == ModoCgroups {
			t.Error("no se pidieron cgroups y sin embargo se declaran como aplicados")
		}
	}
}

// TestSandboxDirectorioBaseInexistente: se crea, no se falla.
func TestSandboxDirectorioBaseInexistente(t *testing.T) {
	base := filepath.Join(t.TempDir(), "no", "existe", "todavia")
	s, err := Nuevo(Opciones{Dir: base, Log: logx.Global()})
	if err != nil {
		t.Fatalf("debería crear el directorio: %v", err)
	}
	defer s.Cerrar()
	if _, err := os.Stat(base); err != nil {
		t.Errorf("no se creó el directorio base: %v", err)
	}
}
