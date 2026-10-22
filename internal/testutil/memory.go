package testutil

import (
	"runtime"
	"time"
)

const heapSampleInterval = 5 * time.Millisecond

// PeakHeapGrowth runs work after a collection and samples live heap growth.
func PeakHeapGrowth(work func() error) (uint64, error) {
	runtime.GC()
	baseline := readHeapAlloc()
	stop := make(chan struct{})
	peakResult := make(chan uint64, 1)
	go sampleHeap(baseline, stop, peakResult)
	err := work()
	close(stop)
	peak := <-peakResult
	if peak < baseline {
		return 0, err
	}
	return peak - baseline, err
}

func sampleHeap(initial uint64, stop <-chan struct{}, result chan<- uint64) {
	ticker := time.NewTicker(heapSampleInterval)
	defer ticker.Stop()
	peak := initial
	for {
		select {
		case <-ticker.C:
			peak = max(peak, readHeapAlloc())
		case <-stop:
			result <- max(peak, readHeapAlloc())
			return
		}
	}
}

func readHeapAlloc() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
