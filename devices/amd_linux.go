//go:build linux
// +build linux

package devices

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	amdVendorID = 0x1002
)

type amdGPU struct {
	name       string
	devicePath string
}

func init() {
	RegisterStartup(startAMD)
}

// updateAMDTemp copies cached AMD GPU temps into temps.
func updateAMDTemp(temps map[string]int) map[string]error {
	amdLock.Lock()
	defer amdLock.Unlock()
	for k, v := range amdTemps {
		temps[k] = v
	}
	return amdErrors
}

// updateAMDMem copies cached AMD GPU memory stats into mems.
func updateAMDMem(mems map[string]MemoryInfo) map[string]error {
	amdLock.Lock()
	defer amdLock.Unlock()
	for k, v := range amdMems {
		mems[k] = v
	}
	return amdErrors
}

// updateAMDUsage copies cached AMD GPU usage into cpus.
func updateAMDUsage(cpus map[string]int, _ bool) map[string]error {
	amdLock.Lock()
	defer amdLock.Unlock()
	for k, v := range amdCpus {
		cpus[k] = v
	}
	return amdErrors
}

func startAMD(vars map[string]string) error {
	enabled := vars["amd"] == "true" || vars["amdgpu"] == "true"
	disabled := vars["amd"] == "false" || vars["amdgpu"] == "false"
	gpus, err := discoverAMDGPUs()
	if err != nil {
		if enabled {
			return err
		}
		return nil
	}
	if len(gpus) == 0 {
		if enabled {
			return errors.New("AMD GPU error: no AMD GPUs found (check /sys/class/drm and /sys/bus/pci/drivers/amdgpu)")
		}
		return nil
	}
	if !enabled {
		if disabled {
			return nil
		}
		// Auto-enable if AMD GPUs are present and not explicitly disabled.
		enabled = true
	}

	amdErrors = make(map[string]error)
	amdTemps = make(map[string]int)
	amdMems = make(map[string]MemoryInfo)
	amdCpus = make(map[string]int)

	RegisterTemp(updateAMDTemp)
	RegisterMem(updateAMDMem)
	RegisterCPU(updateAMDUsage)

	amdLock = sync.Mutex{}
	refresh := time.Second
	if v, ok := vars["amd-refresh"]; ok {
		if refresh, err = time.ParseDuration(v); err != nil {
			return err
		}
	}

	updateAMD()
	go func() {
		timer := time.Tick(refresh)
		for range timer {
			updateAMD()
		}
	}()
	return nil
}

var (
	amdTemps  map[string]int
	amdMems   map[string]MemoryInfo
	amdCpus   map[string]int
	amdErrors map[string]error
)

var amdLock sync.Mutex

func updateAMD() {
	gpus, err := discoverAMDGPUs()
	if err != nil {
		amdLock.Lock()
		if amdErrors == nil {
			amdErrors = make(map[string]error)
		}
		amdErrors["amd"] = err
		amdLock.Unlock()
		return
	}

	temps := make(map[string]int)
	mems := make(map[string]MemoryInfo)
	cpus := make(map[string]int)
	errs := make(map[string]error)

	for _, gpu := range gpus {
		if temp, err := readAMDTemp(gpu.devicePath); err == nil {
			temps[gpu.name] = temp
		} else {
			errs[gpu.name] = err
		}

		if usage, err := readAMDBusy(gpu.devicePath); err == nil {
			cpus[gpu.name] = usage
		} else {
			errs[gpu.name] = err
		}

		if mem, err := readAMDVram(gpu.devicePath); err == nil {
			mems[gpu.name] = mem
		} else {
			errs[gpu.name] = err
		}
	}

	amdLock.Lock()
	amdTemps = temps
	amdMems = mems
	amdCpus = cpus
	amdErrors = errs
	amdLock.Unlock()
}

func discoverAMDGPUs() ([]amdGPU, error) {
	entries, err := os.ReadDir("/sys/class/drm")
	if err != nil {
		return nil, err
	}
	ids, _ := loadAMDGpuIDs()
	gpus := make([]amdGPU, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, "card") {
			continue
		}
		if strings.Contains(name, "-") {
			continue
		}
		devicePath := filepath.Join("/sys/class/drm", name, "device")
		vendor, err := readHexInt(filepath.Join(devicePath, "vendor"))
		if err != nil || vendor != amdVendorID {
			continue
		}
		driverName := driverName(devicePath)
		if driverName != "" && driverName != "amdgpu" && driverName != "radeon" {
			continue
		}
		label := amdLabel(name, devicePath, ids)
		gpus = append(gpus, amdGPU{
			name:       label,
			devicePath: devicePath,
		})
	}
	if len(gpus) == 0 {
		return discoverAMDGPUsFromPCIDevices(ids)
	}
	return gpus, nil
}

func discoverAMDGPUsFromPCIDevices(ids map[amdIDKey]string) ([]amdGPU, error) {
	driverRoot := "/sys/bus/pci/drivers/amdgpu"
	entries, err := os.ReadDir(driverRoot)
	if err != nil {
		return nil, err
	}
	gpus := make([]amdGPU, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || strings.HasPrefix(name, "bind") || strings.HasPrefix(name, "unbind") ||
			strings.HasPrefix(name, "new_id") || strings.HasPrefix(name, "remove_id") ||
			strings.HasPrefix(name, "uevent") {
			continue
		}
		devicePath := filepath.Join("/sys/bus/pci/devices", name)
		if _, err := os.Stat(devicePath); err != nil {
			continue
		}
		vendor, err := readHexInt(filepath.Join(devicePath, "vendor"))
		if err != nil || vendor != amdVendorID {
			continue
		}
		label := amdLabel(name, devicePath, ids)
		gpus = append(gpus, amdGPU{
			name:       label,
			devicePath: devicePath,
		})
	}
	return gpus, nil
}

func amdLabel(card string, devicePath string, ids map[amdIDKey]string) string {
	slot := pciSlotName(devicePath)
	if ids != nil {
		if devID, err := readHexUint(filepath.Join(devicePath, "device")); err == nil {
			if revID, err := readHexUint(filepath.Join(devicePath, "revision")); err == nil {
				if name, ok := ids[amdIDKey{deviceID: devID, revisionID: revID}]; ok && name != "" {
					return formatAMDLabel(name, slot, card)
				}
			}
		}
	}
	return formatAMDLabel("AMD", slot, card)
}

func formatAMDLabel(name string, slot string, card string) string {
	cleanName := simplifyAMDName(name)
	cleanSlot := simplifyPCISlot(slot)
	if cleanSlot != "" {
		return fmt.Sprintf("%s.%s", cleanName, cleanSlot)
	}
	if cleanName != "" && cleanName != "AMD" {
		return cleanName
	}
	return fmt.Sprintf("AMD.%s", card)
}

func simplifyAMDName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	clean = strings.ReplaceAll(clean, "AMD Instinct MI210", "MI210")
	clean = strings.ReplaceAll(clean, "AMD MI210", "MI210")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI250X / MI250", "MI250")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI250X/MI250", "MI250")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI250", "MI250")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI300X", "MI300X")
	clean = strings.ReplaceAll(clean, "AMD MI300X", "MI300X")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI300", "MI300")
	clean = strings.ReplaceAll(clean, "AMD MI300", "MI300")
	clean = strings.ReplaceAll(clean, "AMD Instinct MI325X", "MI325X")
	clean = strings.ReplaceAll(clean, "AMD MI325X", "MI325X")
	return clean
}

func simplifyPCISlot(slot string) string {
	clean := strings.TrimSpace(slot)
	if clean == "" {
		return ""
	}
	clean = strings.TrimPrefix(clean, "0000:")
	return strings.TrimSuffix(clean, ":00.0")
}

func pciSlotName(devicePath string) string {
	uevent, err := os.ReadFile(filepath.Join(devicePath, "uevent"))
	if err == nil {
		lines := strings.Split(string(uevent), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
				return strings.TrimPrefix(line, "PCI_SLOT_NAME=")
			}
		}
	}
	return ""
}

func driverName(devicePath string) string {
	link, err := os.Readlink(filepath.Join(devicePath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(link)
}

func readAMDTemp(devicePath string) (int, error) {
	hwmonPath, err := firstHwmonPath(devicePath)
	if err != nil {
		return 0, err
	}
	tempPath, err := firstMatchingFile(hwmonPath, "temp", "_input")
	if err != nil {
		return 0, err
	}
	val, err := readInt(tempPath)
	if err != nil {
		return 0, err
	}
	return int((val + 500) / 1000), nil
}

func readAMDBusy(devicePath string) (int, error) {
	return readInt(filepath.Join(devicePath, "gpu_busy_percent"))
}

func readAMDVram(devicePath string) (MemoryInfo, error) {
	total, err := readUint(filepath.Join(devicePath, "mem_info_vram_total"))
	if err != nil {
		total, err = readUint(filepath.Join(devicePath, "mem_info_vis_vram_total"))
		if err != nil {
			return MemoryInfo{}, err
		}
	}
	used, err := readUint(filepath.Join(devicePath, "mem_info_vram_used"))
	if err != nil {
		used, err = readUint(filepath.Join(devicePath, "mem_info_vis_vram_used"))
		if err != nil {
			return MemoryInfo{}, err
		}
	}
	if total == 0 {
		return MemoryInfo{}, errors.New("AMD GPU error: total VRAM is zero")
	}
	return MemoryInfo{
		Total:       total,
		Used:        used,
		UsedPercent: (float64(used) / float64(total)) * 100.0,
	}, nil
}

func firstHwmonPath(devicePath string) (string, error) {
	hwmonRoot := filepath.Join(devicePath, "hwmon")
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(hwmonRoot, entry.Name()), nil
		}
	}
	return "", errors.New("AMD GPU error: no hwmon directory")
}

func firstMatchingFile(dir string, prefix string, suffix string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("AMD GPU error: no %s*%s file found", prefix, suffix)
}

func readHexInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	parsed, err := strconv.ParseInt(val, 0, 32)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

type amdIDKey struct {
	deviceID   uint32
	revisionID uint32
}

var (
	amdIDsOnce sync.Once
	amdIDs     map[amdIDKey]string
	amdIDsErr  error
)

func loadAMDGpuIDs() (map[amdIDKey]string, error) {
	amdIDsOnce.Do(func() {
		paths := []string{
			"/usr/share/libdrm/amdgpu.ids",
		}
		var file *os.File
		for _, path := range paths {
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			file = f
			break
		}
		if file == nil {
			amdIDsErr = errors.New("AMD GPU error: amdgpu.ids not found")
			return
		}
		defer file.Close()
		amdIDs = make(map[amdIDKey]string)
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, ",") {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) < 3 {
				continue
			}
			dev := strings.TrimSpace(parts[0])
			rev := strings.TrimSpace(parts[1])
			name := strings.TrimSpace(strings.Join(parts[2:], ","))
			if dev == "" || rev == "" || name == "" {
				continue
			}
			devID, err := strconv.ParseUint(dev, 16, 32)
			if err != nil {
				continue
			}
			revID, err := strconv.ParseUint(rev, 16, 32)
			if err != nil {
				continue
			}
			amdIDs[amdIDKey{deviceID: uint32(devID), revisionID: uint32(revID)}] = name
		}
		if err := scanner.Err(); err != nil {
			amdIDsErr = err
		}
	})
	return amdIDs, amdIDsErr
}

func readHexUint(path string) (uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	parsed, err := strconv.ParseUint(val, 0, 32)
	if err != nil {
		return 0, err
	}
	return uint32(parsed), nil
}

func readInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	parsed, err := strconv.ParseInt(val, 10, 32)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	val := strings.TrimSpace(string(data))
	parsed, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}
