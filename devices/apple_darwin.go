//go:build darwin && cgo
// +build darwin,cgo

package devices

/*
#cgo LDFLAGS: -framework Metal -framework IOKit -framework Foundation
#include <stdlib.h>
#include "apple_gpu_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

type appleGPUInfo struct {
	name  string
	total uint64
	used  uint64
	util  int64
}

func init() {
	RegisterStartup(startAppleGPU)
}

func startAppleGPU(vars map[string]string) error {
	if vars["apple"] != "true" {
		return nil
	}

	appleErrors = make(map[string]error)
	appleMems = make(map[string]MemoryInfo)
	appleCpus = make(map[string]int)

	RegisterMem(updateAppleMem)
	RegisterCPU(updateAppleUsage)

	refresh := time.Second
	if v, ok := vars["apple-refresh"]; ok {
		var err error
		if refresh, err = time.ParseDuration(v); err != nil {
			return err
		}
	}

	infos, err := readAppleGPUs()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return errors.New("Apple GPU error: no Apple GPUs found")
	}
	updateAppleFromInfos(infos)

	go func() {
		timer := time.Tick(refresh)
		for range timer {
			updateApple()
		}
	}()
	return nil
}

func updateAppleMem(mems map[string]MemoryInfo) map[string]error {
	appleLock.Lock()
	defer appleLock.Unlock()
	for k, v := range appleMems {
		mems[k] = v
	}
	return appleErrors
}

func updateAppleUsage(cpus map[string]int, _ bool) map[string]error {
	appleLock.Lock()
	defer appleLock.Unlock()
	for k, v := range appleCpus {
		cpus[k] = v
	}
	return appleErrors
}

func updateApple() {
	infos, err := readAppleGPUs()
	if err != nil {
		appleLock.Lock()
		if appleErrors == nil {
			appleErrors = make(map[string]error)
		}
		appleErrors["apple"] = err
		appleLock.Unlock()
		return
	}
	updateAppleFromInfos(infos)
}

func updateAppleFromInfos(infos []appleGPUInfo) {
	newErrors := make(map[string]error)
	newMems := make(map[string]MemoryInfo, len(infos))
	newCpus := make(map[string]int, len(infos))

	for idx, info := range infos {
		name := fmt.Sprintf("%s.%d", info.name, idx)
		if info.util >= 0 {
			newCpus[name] = int(info.util)
		} else {
			newCpus[name] = 0
		}

		usedPercent := 0.0
		if info.total > 0 {
			usedPercent = (float64(info.used) / float64(info.total)) * 100.0
		}
		newMems[name] = MemoryInfo{
			Total:       info.total,
			Used:        info.used,
			UsedPercent: usedPercent,
		}
	}

	appleLock.Lock()
	appleErrors = newErrors
	appleMems = newMems
	appleCpus = newCpus
	appleLock.Unlock()
}

func readAppleGPUs() ([]appleGPUInfo, error) {
	var cInfos *C.struct_apple_gpu_info
	var cCount C.int
	var cErr *C.char

	rc := C.apple_gpu_get_infos(&cInfos, &cCount, &cErr)
	if rc != 0 {
		defer C.apple_gpu_free_error(cErr)
		if cErr != nil {
			return nil, errors.New(C.GoString(cErr))
		}
		return nil, errors.New("Apple GPU error: unknown failure")
	}
	defer C.apple_gpu_free_infos(cInfos, cCount)

	count := int(cCount)
	if count == 0 {
		return []appleGPUInfo{}, nil
	}

	cSlice := (*[1 << 30]C.struct_apple_gpu_info)(unsafe.Pointer(cInfos))[:count:count]
	infos := make([]appleGPUInfo, 0, count)
	for _, info := range cSlice {
		infos = append(infos, appleGPUInfo{
			name:  C.GoString(info.name),
			total: uint64(info.total_mem),
			used:  uint64(info.used_mem),
			util:  int64(info.gpu_util),
		})
	}
	return infos, nil
}

var (
	appleMems   map[string]MemoryInfo
	appleCpus   map[string]int
	appleErrors map[string]error
)

var appleLock sync.Mutex
