package monitoring

import (
	"testing"
)

func TestGpuName(t *testing.T) {
	name := GpuName()
	if name == "" || name == "Unknown" {
		t.Errorf("Expected GPU name, got empty or 'Unknown'")
	}
	t.Logf("GPU name: %s", name)
}

func TestFormatGPUNameList(t *testing.T) {
	got := formatGPUNameList([]string{
		"NVIDIA GeForce RTX 4090",
		"NVIDIA GeForce RTX 4090",
		"AMD Radeon RX 7900 XTX",
		"  ",
	})
	want := "NVIDIA GeForce RTX 4090 × 2, AMD Radeon RX 7900 XTX"
	if got != want {
		t.Fatalf("formatGPUNameList() = %q, want %q", got, want)
	}
}

func TestFormatGPUNameListEmpty(t *testing.T) {
	if got := formatGPUNameList(nil); got != "None" {
		t.Fatalf("formatGPUNameList(nil) = %q, want None", got)
	}
}

func TestDetailedGPUEmptyFixtures(t *testing.T) {
	amd := &ROCmSMI{data: []byte(`{}`)}
	amdInfo, err := amd.gatherDetailedInfo()
	if err != nil {
		t.Fatalf("empty AMD fixture failed: %v", err)
	}
	if len(amdInfo) != 0 {
		t.Fatalf("empty AMD fixture returned %d GPUs", len(amdInfo))
	}

	nvidia := &NvidiaSMI{data: []byte(`<nvidia_smi_log></nvidia_smi_log>`)}
	nvidiaInfo, err := nvidia.gatherDetailedInfo()
	if err != nil {
		t.Fatalf("empty NVIDIA fixture failed: %v", err)
	}
	if len(nvidiaInfo) != 0 {
		t.Fatalf("empty NVIDIA fixture returned %d GPUs", len(nvidiaInfo))
	}
}

func TestNvidiaDetailedFixture(t *testing.T) {
	smi := &NvidiaSMI{data: []byte(`<nvidia_smi_log>
		<gpu>
			<product_name>NVIDIA Test GPU</product_name>
			<utilization><gpu_util>37.5 %</gpu_util></utilization>
			<fb_memory_usage><total>8192 MiB</total><used>1024 MiB</used></fb_memory_usage>
			<temperature><gpu_temp>65 C</gpu_temp></temperature>
		</gpu>
		<gpu>
			<product_name>NVIDIA Other GPU</product_name>
			<utilization><gpu_util>N/A</gpu_util></utilization>
			<fb_memory_usage><total>4096 MiB</total><used>N/A</used></fb_memory_usage>
			<temperature><gpu_temp>N/A</gpu_temp></temperature>
		</gpu>
	</nvidia_smi_log>`)}
	infos, err := smi.GatherDetailedInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d NVIDIA GPUs, want 2", len(infos))
	}
	want := NVIDIAGPUInfo{Name: "NVIDIA Test GPU", MemoryTotal: 8192 * 1024 * 1024, MemoryUsed: 1024 * 1024 * 1024, Utilization: 37.5, Temperature: 65}
	if infos[0] != want {
		t.Fatalf("first NVIDIA GPU = %+v, want %+v", infos[0], want)
	}
	want = NVIDIAGPUInfo{Name: "NVIDIA Other GPU", MemoryTotal: 4096 * 1024 * 1024}
	if infos[1] != want {
		t.Fatalf("second NVIDIA GPU = %+v, want %+v", infos[1], want)
	}
	models, err := smi.GatherModel()
	if err != nil || len(models) != 2 || models[0] != infos[0].Name || models[1] != infos[1].Name {
		t.Fatalf("NVIDIA models = %v, error = %v", models, err)
	}
	usage, err := smi.GatherUsage()
	if err != nil || len(usage) != 2 || usage[0] != 37.5 || usage[1] != 0 {
		t.Fatalf("NVIDIA utilization = %v, error = %v", usage, err)
	}
}

func TestROCmDetailedFixture(t *testing.T) {
	smi := &ROCmSMI{data: []byte(`{"card0": {
		"Card series": "AMD Test GPU", "GPU use (%)": "42.5",
		"VRAM Total Memory (B)": "4294967296", "VRAM Total Used Memory (B)": "1073741824",
		"Temperature (Sensor junction) (C)": "64"
	}}`)}
	infos, err := smi.GatherDetailedInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := AMDGPUInfo{Name: "AMD Test GPU", MemoryTotal: 4294967296, MemoryUsed: 1073741824, Utilization: 42.5, Temperature: 64}
	if len(infos) != 1 || infos[0] != want {
		t.Fatalf("ROCm GPU info = %+v, want %+v", infos, want)
	}
}
