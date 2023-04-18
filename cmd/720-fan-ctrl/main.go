package main

import (
	"fmt"
	linuxproc "github.com/c9s/goprocinfo/linux"
	"github.com/gin-gonic/gin"
	"github.com/md14454/gosensors"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	currentMaxTemp = float64(0)
	lastPwm        = int64(15)

	cpusTemp []float64
	nvmeTemp []float64

	lastCpuIdle  uint64
	lastCpuTotal uint64

	cpuUsage float64
	upTime   int64

	memFree      uint64
	memAvailable uint64
	memTotal     uint64
	memUsage     float64

	lock = sync.Mutex{}
)

func main() {
	var err error

	_, err = exec.Command("ipmitool", "raw", "0x30", "0x30", "0x01", "0x00").CombinedOutput()
	if err != nil {
		os.Exit(5)
	}
	_, err = exec.Command("ipmitool", "raw", "0x30", "0x30", "0x02", "0xff", "15").CombinedOutput()
	if err != nil {
		os.Exit(5)
	}

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		gosensors.Init()
		defer gosensors.Cleanup()
		defer wg.Done()
		for {
			tempData := getPackageTemp()

			lock.Lock()
			currentMaxTemp = getMaxTemp(tempData)
			getMaxNvemTemp(tempData)

			var result int64
			result = 0
			if currentMaxTemp <= 34 {
				result = 15
			} else if currentMaxTemp >= 70 {
				result = 100
			} else if currentMaxTemp >= 64 {
				result = 60
			} else if currentMaxTemp >= 62 {
				result = 45
			} else if currentMaxTemp >= 60 {
				result = 40
			} else {
				result = 15 + int64((currentMaxTemp-24)/2)
			}
			if result <= 15 {
				result = 15
			}
			if lastPwm != result {
				execStrArg := fmt.Sprintf("%d", result)
				fmt.Println("new fan pwm: " + execStrArg)
				_, err := exec.Command("ipmitool", "raw", "0x30", "0x30", "0x02", "0xff", execStrArg).CombinedOutput()
				if err != nil {
					time.Sleep(time.Second * 5)
					continue
				}
				lastPwm = result
			}

			getSystemInfo()
			getSystemMemoryInfo()
			lock.Unlock()
			time.Sleep(time.Second)
		}
	}()

	go func() {
		defer wg.Done()
		gin.SetMode(gin.ReleaseMode)
		router := gin.New()
		router.Use(gin.Recovery())
		router.GET("/info", func(context *gin.Context) {
			lock.Lock()
			defer lock.Unlock()
			result := struct {
				Temperature float64 `json:"temperature"`
				DutyCycle   int64   `json:"duty_cycle"`

				PerCoreTemperature []float64 `json:"per_core_temperature"`
				PerNvmeTemperature []float64 `json:"per_nvme_temperature"`
				CpuUsage           float64   `json:"cpu_usage"`
				UpTime             int64     `json:"up_time"`

				MemTotal     uint64  `json:"mem_total"`
				MemFree      uint64  `json:"mem_free"`
				MemAvailable uint64  `json:"mem_available"`
				MemUsage     float64 `json:"mem_usage"`
			}{
				Temperature:        currentMaxTemp,
				DutyCycle:          lastPwm,
				PerCoreTemperature: cpusTemp,
				PerNvmeTemperature: nvmeTemp,
				CpuUsage:           cpuUsage,
				UpTime:             upTime,
				MemTotal:           memTotal,
				MemFree:            memFree,
				MemAvailable:       memAvailable,
				MemUsage:           memUsage,
			}

			context.JSON(http.StatusOK, result)
		})
		err = router.Run("0.0.0.0:60001")
		if err != nil {
			os.Exit(5)
		}
	}()

	wg.Wait()
}

func getMaxNvemTemp(tempData *map[string][]map[string]float64) {
	if nvmeData, ok := (*tempData)["nvme"]; ok {
		if len(nvmeData) > 0 {
			nvmeTemp = make([]float64, len(nvmeData))
			for nvmeId, d := range nvmeData {
				nvmeTemp[nvmeId] = -65535
				if len(d) > 0 {
					for _, t := range d {
						if t > nvmeTemp[nvmeId] {
							nvmeTemp[nvmeId] = t
						}
					}
				}
			}
		}
	}
}

func getMaxTemp(tempData *map[string][]map[string]float64) float64 {
	max := float64(-65535)

	if cpuData, ok := (*tempData)["coretemp"]; ok {

		if len(cpuData) > 0 {
			cpusTemp = make([]float64, len(cpuData))
			for cpuId, d := range cpuData {
				cpusTemp[cpuId] = -65535
				if len(d) > 0 {
					for _, t := range d {
						if t > max {
							max = t
						}

						if t > cpusTemp[cpuId] {
							cpusTemp[cpuId] = t
						}
					}
				}
			}
		}
	}

	return max
}

func getPackageTemp() *map[string][]map[string]float64 {
	resultMap := make(map[string][]map[string]float64)
	chips := gosensors.GetDetectedChips()

	for i := range chips {
		chip := chips[i]

		if _, ok := resultMap[chip.Prefix]; !ok {
			resultMap[chip.Prefix] = make([]map[string]float64, 0)
		}

		features := chip.GetFeatures()

		tmpChipData := make(map[string]float64)

		for j := range features {
			feature := features[j]
			tmpChipData[feature.GetLabel()] = feature.GetValue()
		}

		resultMap[chip.Prefix] = append(resultMap[chip.Prefix], tmpChipData)
	}
	return &resultMap
}

func getSystemInfo() {
	stat, err := linuxproc.ReadStat("/proc/stat")
	if err != nil || stat == nil {
		return
	}

	upTime = time.Now().Unix() - stat.BootTime.Unix()

	nowIdle := stat.CPUStatAll.Idle
	nowTotal := stat.CPUStatAll.User + stat.CPUStatAll.System + stat.CPUStatAll.IOWait + stat.CPUStatAll.Idle

	tmpTotal := nowTotal - lastCpuTotal
	tmpIdle := nowIdle - lastCpuIdle

	cpuUsage = math.Trunc((float64(tmpTotal-tmpIdle)/float64(tmpTotal))*10000) / 100

	lastCpuIdle = nowIdle
	lastCpuTotal = nowTotal

}
func getSystemMemoryInfo() {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	content := string(b)
	lines := strings.Split(content, "\n")

	varGets := 0
	for _, line := range lines {
		if varGets >= 3 {
			break
		}

		if strings.HasPrefix(line, "MemTotal") {
			varGets++

			tmp := strings.ReplaceAll(line, "MemTotal:", "")
			tmp = strings.ReplaceAll(tmp, "kB", "")
			tmp = strings.TrimSpace(tmp)
			parseUint, err := strconv.ParseUint(tmp, 10, 64)
			if err == nil {
				memTotal = parseUint
			}
			continue
		}
		if strings.HasPrefix(line, "MemFree") {
			varGets++

			tmp := strings.ReplaceAll(line, "MemFree:", "")
			tmp = strings.ReplaceAll(tmp, "kB", "")
			tmp = strings.TrimSpace(tmp)
			parseUint, err := strconv.ParseUint(tmp, 10, 64)
			if err == nil {
				memFree = parseUint
			}
			continue
		}
		if strings.HasPrefix(line, "MemAvailable") {
			varGets++

			tmp := strings.ReplaceAll(line, "MemAvailable:", "")
			tmp = strings.ReplaceAll(tmp, "kB", "")
			tmp = strings.TrimSpace(tmp)
			parseUint, err := strconv.ParseUint(tmp, 10, 64)
			if err == nil {
				memAvailable = parseUint
			}
			continue
		}
	}

	memUsage = math.Trunc((float64(memTotal-memFree)/float64(memTotal))*10000) / 100

}
