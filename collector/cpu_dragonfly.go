// Copyright 2015 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build !nocpu

package collector

import (
	"errors"
	"fmt"
	"strconv"
	"unsafe"

	"github.com/prometheus/client_golang/prometheus"
)

/*
#cgo LDFLAGS:
#include <fcntl.h>
#include <stdlib.h>
#include <sys/param.h>
#include <sys/resource.h>
#include <sys/time.h>
#include <sys/sysctl.h>
#include <kinfo.h>

static int mibs_set_up = 0;

static int mib_kern_cp_times[2];
static size_t mib_kern_cp_times_len = 2;

static const int mib_hw_ncpu[] = {CTL_HW, HW_NCPU};
static const size_t mib_hw_ncpu_len = 2;

static const int mib_kern_clockrate[] = {CTL_KERN, KERN_CLOCKRATE};
static size_t mib_kern_clockrate_len = 2;

// Setup method for MIBs not available as constants.
// Calls to this method must be synchronized externally.
int setupSysctlMIBs() {
	int ret = sysctlnametomib("kern.cputime", mib_kern_cp_times, &mib_kern_cp_times_len);
	if (ret == 0) mibs_set_up = 1;
	return ret;
}

int getCPUTimes(int *ncpu, struct kinfo_cputime *cputime, uint64_t *cpufreq) {
	// // Assert that mibs are set up through setupSysctlMIBs
	// if (!mibs_set_up) {
	// 	return -1;
	// }

	// // Retrieve number of cpu cores
	// size_t ncpu_size = sizeof(*ncpu);
	// if (sysctl(mib_hw_ncpu, mib_hw_ncpu_len, ncpu, &ncpu_size, NULL, 0) == -1 ||
	//     sizeof(*ncpu) != ncpu_size) {
	// 	return -1;
	// }

	size_t len;

	// Get number of cpu cores.
	int mib[2];
	mib[0] = CTL_HW;
	mib[1] = HW_NCPU;
	len = sizeof(*ncpu);
	if (sysctl(mib, 2, ncpu, &len, NULL, 0)) {
		return -1;
	}

	// Retrieve clockrate
	struct clockinfo clockrate;
	size_t clockrate_size = sizeof(clockrate);
	if (sysctl(mib_kern_clockrate, mib_kern_clockrate_len, &clockrate, &clockrate_size, NULL, 0) == -1 ||
	    sizeof(clockrate) != clockrate_size) {
		return -1;
	}
	*cpufreq = clockrate.stathz > 0 ? clockrate.stathz : clockrate.hz;

	// // Retrieve cp_times values
	// *cp_times_length = (*ncpu) * CPUSTATES;
        //
	// long cp_times[*cp_times_length];
	// size_t cp_times_size = sizeof(cp_times);

	// Get the cpu times.
	struct kinfo_cputime cp_t[*ncpu];
	bzero(cp_t, sizeof(struct kinfo_cputime)*(*ncpu));
	len = sizeof(cp_t[0])*(*ncpu);
	if (sysctlbyname("kern.cputime", &cp_t, &len, NULL, 0)) {
		return -1;
	}

	*cputime = cp_t[0];

	return 0;

}

void freeCPUTimes(double *cpu_times) {
	free(cpu_times);
}

*/
import "C"

const maxCPUTimesLen = C.MAXCPU * C.CPUSTATES

type statCollector struct {
	cpu *prometheus.CounterVec
}

func init() {
	Factories["cpu"] = NewStatCollector
}

// Takes a prometheus registry and returns a new Collector exposing
// CPU stats.
func NewStatCollector() (Collector, error) {
	if C.setupSysctlMIBs() == -1 {
		return nil, errors.New("could not initialize sysctl MIBs")
	}
	return &statCollector{
		cpu: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "cpu_seconds_total",
				Help:      "Seconds the CPU spent in each mode.",
			},
			[]string{"cpu", "mode"},
		),
	}, nil
}

type kinfoCPUTime struct {
	cp_user, cp_nice, cp_sys, cp_intr, cp_idle uint64
}

// Expose CPU stats using sysctl.
func (c *statCollector) Update(ch chan<- prometheus.Metric) (err error) {

	// We want time spent per-cpu per CPUSTATE.
	// CPUSTATES (number of CPUSTATES) is defined as 5U.
	// Order: CP_USER | CP_NICE | CP_SYS | CP_IDLE | CP_INTR
	// sysctl kern.cp_times provides hw.ncpu * CPUSTATES long integers:
	//   hw.ncpu * (space-separated list of the above variables)
	//
	// Each value is a counter incremented at frequency
	//   kern.clockrate.(stathz | hz)
	//
	// Look into sys/kern/kern_clock.c for details.

	var ncpu C.int
	var cpuTimesC C.struct_kinfo_cputime
	var cpuFreq C.uint64_t

	if C.getCPUTimes(&ncpu, &cpuTimesC, &cpuFreq) == -1 {
		return errors.New("could not retrieve CPU times")
	}
	// TODO: Remember to free variables
	// defer C.freeCPUTimes(cpuTimesC)

	cpuTimes := (*[1 << 30]C.struct_kinfo_cputime)(unsafe.Pointer(&cpuTimesC))[:ncpu:ncpu]

	// Sample output:
	// cpu0: {590123223 35166845 334263626 8693757 9845460604 0 0 0 0 [0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0]}
	// cpu1: {0 590123223 35166845 334263626 8693757 9845460604 0 0 0 [0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0]}
	// Figure out why I'm getting the same values here instead of the second CPU
	fmt.Println(cpuTimes)

	for i, cpu := range cpuTimes {
		c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(i), "mode": "user"}).Set(float64(cpu.cp_user / cpuFreq))
		c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(i), "mode": "nice"}).Set(float64(cpu.cp_nice / cpuFreq))
		c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(i), "mode": "system"}).Set(float64(cpu.cp_sys / cpuFreq))
		c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(i), "mode": "interrupt"}).Set(float64(cpu.cp_intr / cpuFreq))
		c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(i), "mode": "idle"}).Set(float64(cpu.cp_idle / cpuFreq))
	}
	// for cpu := 0; cpu < int(ncpu); cpu++ {
	// 	base_idx := C.CPUSTATES * cpu
	// 	c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(cpu), "mode": "user"}).Set(float64(cpuTimes[base_idx+C.CP_USER]))
	// 	c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(cpu), "mode": "nice"}).Set(float64(cpuTimes[base_idx+C.CP_NICE]))
	// 	c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(cpu), "mode": "system"}).Set(float64(cpuTimes[base_idx+C.CP_SYS]))
	// 	c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(cpu), "mode": "interrupt"}).Set(float64(cpuTimes[base_idx+C.CP_INTR]))
	// 	c.cpu.With(prometheus.Labels{"cpu": strconv.Itoa(cpu), "mode": "idle"}).Set(float64(cpuTimes[base_idx+C.CP_IDLE]))
	// }

	c.cpu.Collect(ch)
	return err
}
