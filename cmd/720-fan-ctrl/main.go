package main

import (
	"container/list"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/md14454/gosensors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	currentMaxTemp = int64(0)
	lastPwm        = int64(15)
)

func main() {
	_, err := exec.Command("ipmitool", "raw", "0x30", "0x30", "0x01", "0x00").CombinedOutput()
	if err != nil {
		os.Exit(5)
	}
	_, err = exec.Command("ipmitool", "raw", "0x30", "0x30", "0x02", "0xff", "15").CombinedOutput()
	if err != nil {
		os.Exit(5)
	}

	go func() {
		gosensors.Init()
		defer gosensors.Cleanup()
		for {
			currentMaxTemp = int64(getMaxTemp())
			var result int64
			result = 0
			if currentMaxTemp <= 34 {
				result = 15
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
			time.Sleep(time.Second)
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/info", func(context *gin.Context) {
		context.JSON(http.StatusOK, struct {
			Temperature int64 `json:"temperature"`
			DutyCycle   int64 `json:"duty_cycle"`
		}{
			Temperature: currentMaxTemp,
			DutyCycle:   lastPwm,
		})
	})
	err = router.Run("0.0.0.0:60001")
	if err != nil {
		os.Exit(5)
	}
}

func getMaxTemp() float64 {
	var max float64
	max = -65535
	tempList := getPackageTemp()
	for e := tempList.Front(); e != nil; e = e.Next() {
		tmp := e.Value.(float64)
		if tmp > max {
			max = tmp
		}
	}
	return max
}

func getPackageTemp() *list.List {
	result := list.New()
	chips := gosensors.GetDetectedChips()
	for i := range chips {
		chip := chips[i]
		features := chip.GetFeatures()
		for j := range features {
			feature := features[j]
			if strings.Contains(feature.GetLabel(), "Package") {
				result.PushBack(feature.GetValue())
			}
		}
	}
	return result
}
