package app

import (
	"runtime"
	runtimemetrics "runtime/metrics"
	"sort"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	performanceBaselineOperations        = 2048
	performanceMinThroughputPerSecond    = 10_000
	performanceMaxP95Latency             = 5 * time.Millisecond
	performanceMaxP99Latency             = 10 * time.Millisecond
	performanceMaxHeapGrowthBytes        = 32 << 20
	performanceMaxCPUSecondsPerOperation = 0.002
)

func TestWSClientQueuePerformanceBaseline(t *testing.T) {
	connection := newQueuedTestConnection(true)
	client := &wsClient{
		conn: connection, typeID: websocket.BinaryMessage,
		queueSize:      performanceBaselineOperations + 1,
		queueByteLimit: 8 << 20, maxMessageBytes: 4 << 10,
	}
	payload := make([]byte, 1024)
	if err := client.Send(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("performance writer did not block")
	}
	defer client.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuBefore := totalCPUSeconds()
	latencies := make([]time.Duration, 0, performanceBaselineOperations)
	peakQueueEntries := 0
	peakQueueBytes := int64(0)
	started := time.Now()
	for i := 0; i < performanceBaselineOperations; i++ {
		operationStarted := time.Now()
		if err := client.Send(payload); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		latencies = append(latencies, time.Since(operationStarted))
		client.sendMu.Lock()
		entries, bytes := len(client.outbound), client.queueBytes
		client.sendMu.Unlock()
		if entries > peakQueueEntries {
			peakQueueEntries = entries
		}
		if bytes > peakQueueBytes {
			peakQueueBytes = bytes
		}
	}
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	runtime.GC()
	cpuSeconds := totalCPUSeconds() - cpuBefore

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := percentileDuration(latencies, 95)
	p99 := percentileDuration(latencies, 99)
	throughput := float64(performanceBaselineOperations) / elapsed.Seconds()
	heapGrowth := uint64(0)
	if after.HeapAlloc > before.HeapAlloc {
		heapGrowth = after.HeapAlloc - before.HeapAlloc
	}
	cpuPerOperation := cpuSeconds / performanceBaselineOperations
	t.Logf("performance_baseline operations=%d throughput_ops_s=%.0f p95=%s p99=%s heap_growth_bytes=%d queue_peak_entries=%d queue_peak_bytes=%d cpu_seconds=%.6f cpu_seconds_op=%.9f",
		performanceBaselineOperations, throughput, p95, p99, heapGrowth, peakQueueEntries, peakQueueBytes, cpuSeconds, cpuPerOperation)

	if throughput < performanceMinThroughputPerSecond {
		t.Fatalf("throughput %.0f ops/s below %d", throughput, performanceMinThroughputPerSecond)
	}
	if p95 > performanceMaxP95Latency || p99 > performanceMaxP99Latency {
		t.Fatalf("latency p95=%s p99=%s exceeds %s/%s", p95, p99, performanceMaxP95Latency, performanceMaxP99Latency)
	}
	if heapGrowth > performanceMaxHeapGrowthBytes {
		t.Fatalf("heap growth %d exceeds %d", heapGrowth, performanceMaxHeapGrowthBytes)
	}
	if peakQueueEntries > performanceBaselineOperations || peakQueueBytes > int64((performanceBaselineOperations+1)*len(payload)) {
		t.Fatalf("queue watermark entries=%d bytes=%d exceeds configured workload", peakQueueEntries, peakQueueBytes)
	}
	if cpuPerOperation > performanceMaxCPUSecondsPerOperation {
		t.Fatalf("CPU %.9f seconds/op exceeds %.9f", cpuPerOperation, performanceMaxCPUSecondsPerOperation)
	}
}

func totalCPUSeconds() float64 {
	samples := []runtimemetrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}}
	runtimemetrics.Read(samples)
	return samples[0].Value.Float64()
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	index := (len(sorted)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}
