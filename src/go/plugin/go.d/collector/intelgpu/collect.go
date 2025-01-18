// SPDX-License-Identifier: GPL-3.0-or-later

package intelgpu

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type (
	gpuSummaryStats struct {
		Frequency struct {
			Actual float64 `json:"actual"`
		} `json:"frequency"`
		Power struct {
			GPU float64 `json:"gpu"`
		} `json:"power"`
		Memory struct {
			Actual      float64 `json:"actual"`
			Utilization float64 `json:"utilization"`
		} `json:"memory"`
	}
)

const precision = 100

func (c *Collector) collect() (map[string]int64, error) {
	if c.exec == nil {
		return nil, errors.New("collector not initialized")
	}

	stats, err := c.getGPUSummaryStats()
	if err != nil {
		return nil, err
	}

	mx := make(map[string]int64)

	mx["frequency_actual"] = int64(stats.Frequency.Actual * precision)
	mx["power_gpu"] = int64(stats.Power.GPU * precision)
	mx["memory_actual"] = int64(stats.Memory.Actual)
	//mx["power_package"] = int64(stats.Power.Package * precision)

	//for name, es := range stats.Engines {
	//	if !c.engines[name] {
	//		c.addEngineCharts(name)
	//		c.engines[name] = true
	//	}
	//
	//	key := fmt.Sprintf("engine_%s_busy", name)
	//	mx[key] = int64(es.Busy * precision)
	//}

	return mx, nil
}
func (c *Collector) getGPUSummaryStats() (*gpuSummaryStats, error) {
	bs, err := c.exec.queryGPUSummaryJson()
	if err != nil {
		return nil, err
	}

	if len(bs) == 0 {
		return nil, errors.New("query returned empty response")
	}

	var stats gpuSummaryStats
	if err := parseStatsLine(bs, &stats); err != nil {
		return nil, err
	}

	////if len(stats.Engines) == 0 {
	//	return nil, errors.New("query returned unexpected response")
	//}

	return &stats, nil
}

func parseStatsLine(bytes []byte, stats *gpuSummaryStats) error {
	line := string(bytes)
	fields := strings.Split(line, ",")
	if len(fields) != 6 {
		return fmt.Errorf("invalid number of fields in line: %s", line) // Return zero value
	}

	// Trim spaces from all fields
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}

	// Parse Memory Actual and Utilization
	memoryActual, err := strconv.ParseFloat(fields[5], 64)
	if err != nil {
		return fmt.Errorf("failed to parse memory actual: %v", err)
	}

	memoryUtilization, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return fmt.Errorf("failed to parse memory utilization: %v", err)
	}

	power, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return fmt.Errorf("failed to parse power: %v", err) // Return zero value
	}

	frequency, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return fmt.Errorf("failed to parse frequency: %v", err) // Return zero value
	}

	stats.Memory.Actual = memoryActual
	stats.Memory.Utilization = memoryUtilization
	stats.Power.GPU = power
	stats.Frequency.Actual = frequency

	return nil
}
