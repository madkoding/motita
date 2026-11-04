package localmodel

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// HOW MUCH VIDEO MEMORY THIS MACHINE HAS.
//
// The local model only makes sense on a GPU: on the CPU an 8B model answers a word at a time. The
// probe asks what each platform can say without anything installed beyond the GPU's own driver:
//
//   - NVIDIA (Linux, Windows): nvidia-smi, which every NVIDIA driver ships.
//   - AMD (Linux): the amdgpu driver's counters under /sys/class/drm.
//   - Any GPU (Windows): the adapter's memory in the registry, read through PowerShell. It is the
//     total only; WMI's AdapterRAM stops at 4 GB, the registry does not.
//   - Apple Silicon (macOS): memory is unified, and Metal lets the GPU use about two thirds of it
//     (three quarters above 36 GB), so that share is what is reported.
//   - Intel Macs: system_profiler's VRAM line.
//
// Every step is best effort: a missing tool or file is just "nothing found here".

// GPU is one graphics adapter and its memory, in MiB. Free is 0 when the platform cannot say.
type GPU struct {
	Name    string `json:"name"`
	Vendor  string `json:"vendor"`
	TotalMB int    `json:"total_mb"`
	FreeMB  int    `json:"free_mb,omitempty"`
	// Unified says the memory is shared with the system (Apple Silicon).
	Unified bool `json:"unified,omitempty"`
	// Source names where the numbers came from (nvidia-smi, sysfs, registry, sysctl...).
	Source string `json:"source"`
}

// probe is what detection reads, as fields so a test can play any platform.
type probe struct {
	goos, goarch string
	run          func(ctx context.Context, name string, args ...string) ([]byte, error)
	readFile     func(string) ([]byte, error)
	glob         func(string) ([]string, error)
}

// probeTimeout bounds one external command: a hung driver tool must not hold the settings page.
const probeTimeout = 8 * time.Second

func systemProbe() probe {
	return probe{
		goos: runtime.GOOS, goarch: runtime.GOARCH,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Output()
		},
		readFile: os.ReadFile,
		glob:     filepath.Glob,
	}
}

// DetectGPU is the adapter with the most memory, or nil when none could be read.
func DetectGPU(ctx context.Context) *GPU {
	return systemProbe().detect(ctx)
}

func (p probe) detect(ctx context.Context) *GPU {
	var found []GPU
	switch p.goos {
	case "linux":
		found = append(p.nvidia(ctx), p.amdSysfs()...)
	case "windows":
		found = p.nvidia(ctx)
		if len(found) == 0 {
			found = p.windowsRegistry(ctx)
		}
	case "darwin":
		found = p.mac(ctx)
	}
	return best(found)
}

// best is the GPU with the most memory.
func best(list []GPU) *GPU {
	var top *GPU
	for i := range list {
		if list[i].TotalMB > 0 && (top == nil || list[i].TotalMB > top.TotalMB) {
			top = &list[i]
		}
	}
	return top
}

// nvidia reads nvidia-smi's CSV: "name, total, free" in MiB, one line per GPU.
func (p probe) nvidia(ctx context.Context) []GPU {
	out, err := p.run(ctx, "nvidia-smi", "--query-gpu=name,memory.total,memory.free",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil
	}
	return parseNvidiaSMI(string(out))
}

func parseNvidiaSMI(out string) []GPU {
	var list []GPU
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, ",")
		if len(f) != 3 {
			continue
		}
		total, err1 := strconv.Atoi(strings.TrimSpace(f[1]))
		free, err2 := strconv.Atoi(strings.TrimSpace(f[2]))
		if err1 != nil || total <= 0 {
			continue
		}
		if err2 != nil {
			free = 0
		}
		list = append(list, GPU{Name: strings.TrimSpace(f[0]), Vendor: "nvidia", TotalMB: total,
			FreeMB: free, Source: "nvidia-smi"})
	}
	return list
}

// amdSysfs reads the amdgpu driver's counters (bytes) for each card.
func (p probe) amdSysfs() []GPU {
	dirs, _ := p.glob("/sys/class/drm/card*/device")
	var list []GPU
	seen := map[string]bool{}
	for _, d := range dirs {
		total := p.readInt(filepath.Join(d, "mem_info_vram_total"))
		if total <= 0 || seen[d] {
			continue
		}
		seen[d] = true
		used := p.readInt(filepath.Join(d, "mem_info_vram_used"))
		name := strings.TrimSpace(p.readText(filepath.Join(d, "product_name")))
		if name == "" {
			name = "AMD GPU"
		}
		g := GPU{Name: name, Vendor: "amd", TotalMB: int(total >> 20), Source: "sysfs"}
		if used >= 0 && used <= total {
			g.FreeMB = int((total - used) >> 20)
		}
		list = append(list, g)
	}
	return list
}

func (p probe) readText(path string) string {
	b, err := p.readFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// readInt is the number a sysfs file holds, or -1.
func (p probe) readInt(path string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(p.readText(path)), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// windowsRegistryScript prints "name|bytes" for each display adapter the registry knows.
const windowsRegistryScript = `Get-ItemProperty -Path 'HKLM:\SYSTEM\ControlSet001\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}\0*' ` +
	`-ErrorAction SilentlyContinue | ForEach-Object { '{0}|{1}' -f $_.DriverDesc, $_.'HardwareInformation.qwMemorySize' }`

func (p probe) windowsRegistry(ctx context.Context) []GPU {
	out, err := p.run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", windowsRegistryScript)
	if err != nil {
		return nil
	}
	return parseRegistry(string(out))
}

func parseRegistry(out string) []GPU {
	var list []GPU
	for _, line := range strings.Split(out, "\n") {
		name, size, ok := strings.Cut(strings.TrimSpace(line), "|")
		bytes, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		if !ok || err != nil || bytes <= 0 {
			continue
		}
		list = append(list, GPU{Name: strings.TrimSpace(name), Vendor: vendorOf(name),
			TotalMB: int(bytes >> 20), Source: "registry"})
	}
	return list
}

// vendorOf guesses the vendor from an adapter's name.
func vendorOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "nvidia"), strings.Contains(n, "geforce"), strings.Contains(n, "rtx"):
		return "nvidia"
	case strings.Contains(n, "amd"), strings.Contains(n, "radeon"):
		return "amd"
	case strings.Contains(n, "intel"):
		return "intel"
	case strings.Contains(n, "apple"):
		return "apple"
	default:
		return ""
	}
}

func (p probe) mac(ctx context.Context) []GPU {
	if p.goarch == "arm64" {
		out, err := p.run(ctx, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return nil
		}
		bytes, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil || bytes <= 0 {
			return nil
		}
		return []GPU{{Name: "Apple Silicon", Vendor: "apple", TotalMB: metalShareMB(bytes),
			Unified: true, Source: "sysctl"}}
	}
	out, err := p.run(ctx, "system_profiler", "SPDisplaysDataType", "-json")
	if err != nil {
		return nil
	}
	return parseSystemProfiler(out)
}

// metalShareMB is how much of the unified memory Metal lets the GPU keep (its
// recommendedMaxWorkingSetSize): two thirds, three quarters above 36 GB.
func metalShareMB(bytes int64) int {
	mb := bytes >> 20
	if mb > 36*1024 {
		return int(mb * 3 / 4)
	}
	return int(mb * 2 / 3)
}

func parseSystemProfiler(out []byte) []GPU {
	var doc struct {
		Displays []map[string]any `json:"SPDisplaysDataType"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil
	}
	var list []GPU
	for _, d := range doc.Displays {
		name, _ := d["sppci_model"].(string)
		vram, _ := d["spdisplays_vram"].(string)
		if vram == "" {
			vram, _ = d["spdisplays_vram_shared"].(string)
		}
		if mb := parseSizeMB(vram); mb > 0 {
			list = append(list, GPU{Name: name, Vendor: vendorOf(name), TotalMB: mb, Source: "system_profiler"})
		}
	}
	return list
}

// parseSizeMB reads "8 GB" or "1536 MB".
func parseSizeMB(s string) int {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0
	}
	n, err := strconv.Atoi(f[0])
	if err != nil {
		return 0
	}
	switch strings.ToUpper(f[1]) {
	case "GB":
		return n * 1024
	case "MB":
		return n
	default:
		return 0
	}
}
