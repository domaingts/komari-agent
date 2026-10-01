//go:build windows

package monitoring

import "errors"

// DetailedGPUInfo contains per-device GPU telemetry.
type DetailedGPUInfo struct {
	Name        string  `json:"name"`
	MemoryTotal uint64  `json:"memory_total"` // Bytes.
	MemoryUsed  uint64  `json:"memory_used"`  // Bytes.
	Utilization float64 `json:"utilization"`  // Percent.
	Temperature uint64  `json:"temperature"`  // Celsius.
}

// GetDetailedGPUHost returns NVIDIA GPU models using nvidia-smi.
func GetDetailedGPUHost() ([]string, error) {
	smi := &NvidiaSMI{}
	if err := smi.Start(); err != nil {
		return nil, err
	}
	return smi.GatherModel()
}

// GetDetailedGPUState returns NVIDIA GPU utilization using nvidia-smi.
func GetDetailedGPUState() ([]float64, error) {
	smi := &NvidiaSMI{}
	if err := smi.Start(); err != nil {
		return nil, err
	}
	return smi.GatherUsage()
}

// GetDetailedGPUInfo returns NVIDIA GPU telemetry using nvidia-smi.
func GetDetailedGPUInfo() ([]DetailedGPUInfo, error) {
	smi := &NvidiaSMI{}
	if err := smi.Start(); err != nil {
		return nil, err
	}
	data, err := smi.GatherDetailedInfo()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("no NVIDIA GPU detected")
	}

	infos := make([]DetailedGPUInfo, len(data))
	for i, info := range data {
		infos[i] = DetailedGPUInfo{
			Name:        info.Name,
			MemoryTotal: info.MemoryTotal,
			MemoryUsed:  info.MemoryUsed,
			Utilization: info.Utilization,
			Temperature: info.Temperature,
		}
	}
	return infos, nil
}
