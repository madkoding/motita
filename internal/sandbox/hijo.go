package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// ---------------------------------------------------------------------------
// Proceso hijo de aislamiento.
//
// Go no puede aplicar setrlimit a un hijo desde SysProcAttr, y depender del
// utilitario `prlimit` ataría el agente a que util-linux esté instalado en la
// máquina i386 (a menudo no lo está). La solución sin dependencias es volver a
// ejecutar el propio binario con un modo oculto que:
//
//	1. aplica los setrlimit,
//	2. entra al chroot si se pidió,
//	3. baja privilegios si se pidió,
//	4. y hace syscall.Exec del comando real, sin dejar ningún proceso Go vivo
//	   entre medias (nada que consuma memoria del presupuesto limitado).
// ---------------------------------------------------------------------------

// MarcaHijo es el primer argumento que activa el modo hijo de aislamiento.
const MarcaHijo = "__sandbox_exec"

// Espec describe lo que debe hacer el proceso hijo.
type Espec struct {
	Comando   string   `json:"comando"`
	Args      []string `json:"args"`
	Dir       string   `json:"dir"`
	Limites   Limites  `json:"limites"`
	Chroot    string   `json:"chroot,omitempty"`
	DirChroot string   `json:"dir_chroot,omitempty"`
	DropPrivs bool     `json:"drop_privs,omitempty"`
	Uid       int      `json:"uid,omitempty"`
	Gid       int      `json:"gid,omitempty"`
	Entorno   []string `json:"entorno,omitempty"`
}

// Codificar serializa la especificación para pasarla por línea de comandos.
func (e Espec) Codificar() (string, error) {
	datos, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("no se pudo serializar la especificación del sandbox: %w", err)
	}
	return string(datos), nil
}

// EsEjecucionHijo indica si el programa fue invocado en modo hijo.
func EsEjecucionHijo(args []string) bool {
	return len(args) > 1 && args[0] == MarcaHijo
}

// EjecutarComoHijo es el punto de entrada del modo hijo. Nunca devuelve si todo
// va bien: reemplaza el proceso actual con el comando pedido.
func EjecutarComoHijo(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%s requiere la especificación JSON como argumento", MarcaHijo)
	}
	var espec Espec
	if err := json.Unmarshal([]byte(args[1]), &espec); err != nil {
		return fmt.Errorf("especificación del sandbox ilegible: %w", err)
	}
	if espec.Comando == "" {
		return fmt.Errorf("la especificación del sandbox no trae comando")
	}

	if err := aplicarRlimits(espec.Limites); err != nil {
		return err
	}

	// chroot antes que chdir final: después del chroot la ruta se interpreta
	// dentro de la nueva raíz.
	if espec.Chroot != "" {
		// entrarChroot ya hace el chdir dentro de la nueva raíz.
		if err := entrarChroot(espec.Chroot, espec.DirChroot); err != nil {
			return err
		}
	} else if espec.Dir != "" {
		if err := os.Chdir(espec.Dir); err != nil {
			return fmt.Errorf("chdir(%q): %w", espec.Dir, err)
		}
	}

	if espec.DropPrivs {
		// Los privilegios se bajan al final: chroot necesita root.
		if err := bajarPrivilegios(espec.Uid, espec.Gid); err != nil {
			return err
		}
	}

	entorno := espec.Entorno
	if entorno == nil {
		entorno = os.Environ()
	}

	// syscall.Exec NO busca en PATH: necesita una ruta absoluta. Un `comando: make`
	// o `comando: go` escrito en el YAML fallaría con ENOENT si no se resuelve
	// aquí. Se resuelve dentro del hijo para usar el PATH restringido del
	// sandbox (el mismo que verá el comando), no el del agente.
	ruta := espec.Comando
	if !filepath.IsAbs(ruta) {
		resuelta, err := exec.LookPath(ruta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "starlight: no se encontró el comando %q en el PATH del sandbox (%s): %v\n",
				ruta, os.Getenv("PATH"), err)
			os.Exit(127)
		}
		ruta = resuelta
	}

	// syscall.Exec reemplaza la imagen del proceso: no quedan dos procesos Go.
	if err := syscall.Exec(ruta, append([]string{ruta}, espec.Args...), entorno); err != nil {
		// El error de exec se informa con el código reservado 127 para que el
		// padre pueda distinguirlo de un fallo real del comando.
		fmt.Fprintf(os.Stderr, "starlight: no se pudo ejecutar %q: %v\n", ruta, err)
		os.Exit(127)
	}
	return nil // inalcanzable
}
