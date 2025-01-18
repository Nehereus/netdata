// SPDX-License-Identifier: GPL-3.0-or-later

package intelgpu

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/netdata/netdata/go/plugins/logger"
)

type intelGpuTop interface {
	queryGPUSummaryJson() ([]byte, error)
	stop() error
}

func newIntelGpuTopExec(log *logger.Logger, ndsudoPath string, updateEvery int, device string) (*intelGpuTopExec, error) {
	topExec := &intelGpuTopExec{
		Logger:             log,
		ndsudoPath:         ndsudoPath,
		updateEvery:        updateEvery,
		device:             device,
		firstSampleTimeout: time.Second * 3,
	}

	if err := topExec.run(); err != nil {
		return nil, err
	}

	return topExec, nil
}

type intelGpuTopExec struct {
	*logger.Logger

	ndsudoPath         string
	updateEvery        int
	device             string
	firstSampleTimeout time.Duration

	cmd  *exec.Cmd
	done chan struct{}

	mux        sync.Mutex
	lastSample string
}

func (e *intelGpuTopExec) run() error {
	var cmd *exec.Cmd
	/*
		  0. GPU Utilization (%), GPU active time of the elapsed time, per tile or device. Device-level is the average value of tiles for multi-tiles.
		  1. GPU Power (W), per tile or device.
		  2. GPU Frequency (MHz), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  3. GPU Core Temperature (Celsius Degree), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  4. GPU Memory Temperature (Celsius Degree), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  5. GPU Memory Utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  6. GPU Memory Read (kB/s), per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  7. GPU Memory Write (kB/s), per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  8. GPU Energy Consumed (J), per tile or device.
		  9. GPU EU Array Active (%), the normalized sum of all cycles on all EUs that were spent actively executing instructions. Per tile or device. Device-level is the average value of tiles for multi-tiles.
		  10. GPU EU Array Stall (%), the normalized sum of all cycles on all EUs during which the EUs were stalled.
			  At least one thread is loaded, but the EU is stalled. Per tile or device. Device-level is the average value of tiles for multi-tiles.
		  11. GPU EU Array Idle (%), the normalized sum of all cycles on all cores when no threads were scheduled on a core. Per tile or device. Device-level is the average value of tiles for multi-tiles.
		  12. Reset Counter, per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  13. Programming Errors, per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  14. Driver Errors, per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  15. Cache Errors Correctable, per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  16. Cache Errors Uncorrectable, per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  17. GPU Memory Bandwidth Utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  18. GPU Memory Used (MiB), per tile or device. Device-level is the sum value of tiles for multi-tiles.
		  19. PCIe Read (kB/s), per device.
		  20. PCIe Write (kB/s), per device.
		  21. Xe Link Throughput (kB/s), a list of tile-to-tile Xe Link throughput.
		  22. Compute engine utilizations (%), per tile.
		  23. Render engine utilizations (%), per tile.
		  24. Media decoder engine utilizations (%), per tile.
		  25. Media encoder engine utilizations (%), per tile.
		  26. Copy engine utilizations (%), per tile.
		  27. Media enhancement engine utilizations (%), per tile.
		  28. 3D engine utilizations (%), per tile.
		  29. GPU Memory Errors Correctable, per tile or device. Other non-compute correctable errors are also included. Device-level is the sum value of tiles for multi-tiles.
		  30. GPU Memory Errors Uncorrectable, per tile or device. Other non-compute uncorrectable errors are also included. Device-level is the sum value of tiles for multi-tiles.
		  31. Compute engine group utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  32. Render engine group utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  33. Media engine group utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  34. Copy engine group utilization (%), per tile or device. Device-level is the average value of tiles for multi-tiles.
		  35. Throttle reason, per tile.
		  36. Media Engine Frequency (MHz), per tile or device. Device-level is the average value of tiles for multi-tiles.
	*/
	const modules = "1,2,5,18" //Power, Frequency,Memory Utilization, Memory Used
	if e.device != "" {
		cmd = exec.Command(e.ndsudoPath, "xpum-device-dump", "--device", e.device, "--modules", modules)
	} else {
		cmd = exec.Command(e.ndsudoPath, "xpum-dump", "--modules", modules)
	}

	e.Debugf("executing '%s'", cmd)

	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	firstSample := make(chan struct{}, 1)
	done := make(chan struct{})
	e.cmd = cmd
	e.done = done

	go func() {
		defer close(done)
		sc := bufio.NewScanner(r)
		//var buf bytes.Buffer
		var n int

		// Skip header line if present
		if sc.Scan() && !strings.Contains(sc.Text(), ":") {
			// Skip the header
		}
		for sc.Scan() {
			if n++; n > 1000 {
				break
			}

			text := sc.Text()

			if text == "" {
				continue
			}

			if !strings.Contains(text, ",") {
				continue
			}

			e.mux.Lock()
			e.lastSample = text
			e.mux.Unlock()

			select {
			case firstSample <- struct{}{}:
			default:
			}
			//if text[0] == '}' {
			//	e.mux.Lock()
			//	e.lastSample = buf.String()
			//	e.mux.Unlock()
			//
			//	select {
			//	case firstSample <- struct{}{}:
			//	default:
			//	}
			//
			//	buf.Reset()
			//	n = 0
			//}
		}
	}()

	select {
	case <-e.done:
		_ = e.stop()
		return errors.New("process exited before the first sample was collected")
	case <-time.After(e.firstSampleTimeout):
		_ = e.stop()
		return errors.New("timed out waiting for first sample")
	case <-firstSample:
		return nil
	}
}

func (e *intelGpuTopExec) queryGPUSummaryJson() ([]byte, error) {
	select {
	case <-e.done:
		return nil, errors.New("process has already exited")
	default:
	}

	e.mux.Lock()
	defer e.mux.Unlock()

	return []byte(e.lastSample), nil
}

func (e *intelGpuTopExec) stop() error {
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}

	_ = e.cmd.Process.Kill()
	_ = e.cmd.Wait()
	e.cmd = nil

	select {
	case <-e.done:
		return nil
	case <-time.After(time.Second * 2):
		return errors.New("timed out waiting for process to exit")
	}
}

func (e *intelGpuTopExec) calcIntervalArg() string {
	// intel_gpu_top appends the end marker ("},\n") of the previous sample to the beginning of the next sample.
	// interval must be < than 'firstSampleTimeout'
	interval := 900
	if m := min(e.updateEvery, int(e.firstSampleTimeout.Seconds())); m > 1 {
		interval = m*1000 - 500 // milliseconds
	}
	return strconv.Itoa(interval)
}
