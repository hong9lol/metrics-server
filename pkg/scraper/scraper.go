// Copyright 2018 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package scraper

import (
	"context"
	"errors"
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	apitypes "k8s.io/apimachinery/pkg/types"
	v1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/component-base/metrics"
	"k8s.io/klog/v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/metrics-server/pkg/scraper/client"
	"sigs.k8s.io/metrics-server/pkg/storage"
)

const (
	maxDelayMs       = 4 * 1000
	delayPerSourceMs = 8
)

var (
	_ctx            context.Context = nil
	_node           *corev1.Node    = nil
	requestDuration                 = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Namespace: "metrics_server",
			Subsystem: "kubelet",
			Name:      "request_duration_seconds",
			Help:      "Duration of requests to Kubelet API in seconds",
			Buckets:   metrics.DefBuckets,
		},
		[]string{"node"},
	)
	requestTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Namespace: "metrics_server",
			Subsystem: "kubelet",
			Name:      "request_total",
			Help:      "Number of requests sent to Kubelet API",
		},
		[]string{"success"},
	)
	lastRequestTime = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Namespace: "metrics_server",
			Subsystem: "kubelet",
			Name:      "last_request_time_seconds",
			Help:      "Time of last request performed to Kubelet API since unix epoch in seconds",
		},
		[]string{"node"},
	)
)

// RegisterScraperMetrics registers rate, errors, and duration metrics on
// Kubelet API scrapes.
func RegisterScraperMetrics(registrationFunc func(metrics.Registerable) error) error {
	for _, metric := range []metrics.Registerable{
		requestDuration,
		requestTotal,
		lastRequestTime,
	} {
		err := registrationFunc(metric)
		if err != nil {
			return err
		}
	}
	return nil
}

func fakeNode() {

}

func NewScraper(nodeLister v1listers.NodeLister, client client.KubeletMetricsGetter, scrapeTimeout time.Duration, labelRequirement []labels.Requirement) *scraper {
	labelSelector := labels.Everything()
	if labelRequirement != nil {
		labelSelector = labelSelector.Add(labelRequirement...)
	}

	return &scraper{
		nodeLister:    nodeLister,
		kubeletClient: client,
		scrapeTimeout: scrapeTimeout,
		labelSelector: labelSelector,
	}
}

type scraper struct {
	nodeLister    v1listers.NodeLister
	kubeletClient client.KubeletMetricsGetter
	scrapeTimeout time.Duration
	labelSelector labels.Selector
}

var _ Scraper = (*scraper)(nil)

// NodeInfo contains the information needed to identify and connect to a particular node
// (node name and preferred address).
type NodeInfo struct {
	Name           string
	ConnectAddress string
}

func (c *scraper) Scrape(baseCtx context.Context) *storage.MetricsBatch {

	startTime := myClock.Now()
	nodes, err := c.nodeLister.List(c.labelSelector)

	// make fake nodes
	_nodes := makeFakeNodes(5000)
	nodes = append(nodes, _nodes...)

	if err != nil {
		// report the error and continue on in case of partial results
		klog.ErrorS(err, "Failed to list nodes")
	}
	// klog.Info("Scraping metrics from nodes", "nodes", klog.KObjSlice(nodes), "nodeCount", len(nodes), "nodeSelector", c.labelSelector)

	responseChannel := make(chan *storage.MetricsBatch, len(nodes))
	defer close(responseChannel)

	// startTime := myClock.Now()

	// TODO(serathius): re-evaluate this code -- do we really need to stagger fetches like this?
	// delayMs := delayPerSourceMs * len(nodes)
	// if delayMs > maxDelayMs {
	// 	delayMs = maxDelayMs
	// }

	for _, node := range nodes {
		go func(node *corev1.Node) {
			rand.Seed(time.Now().UnixNano())
			// Prevents network congestion.
			// sleepDuration := time.Duration(rand.Intn(delayMs)) * time.Millisecond
			// time.Sleep(sleepDuration)
			// make the timeout a bit shorter to account for staggering, so we still preserve
			// the overall timeout
			ctx, cancelTimeout := context.WithTimeout(baseCtx, c.scrapeTimeout)
			defer cancelTimeout()
			m, err := c.collectNode(ctx, node)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					klog.ErrorS(err, "Failed to scrape node, timeout to access kubelet", "node", klog.KObj(node), "timeout", c.scrapeTimeout)
				} else {
					klog.ErrorS(err, "Failed to scrape node", "node", klog.KObj(node))
				}
			}
			responseChannel <- m
		}(node)
	}

	res := &storage.MetricsBatch{
		Nodes: map[string]storage.MetricsPoint{},
		Pods:  map[apitypes.NamespacedName]storage.PodMetricsPoint{},
	}

	// klog.Info("%+v\n TTT", len(nodes))
	for range nodes {
		srcBatch := <-responseChannel
		if srcBatch == nil {
			continue
		}
		for nodeName, nodeMetricsPoint := range srcBatch.Nodes {
			// if _, nodeFind := res.Nodes[nodeName]; nodeFind {
			// 	klog.ErrorS(nil, "Got duplicate node point", "node", klog.KRef("", nodeName))
			// 	continue
			// }
			res.Nodes[nodeName] = nodeMetricsPoint
		}
		for podRef, podMetricsPoint := range srcBatch.Pods {
			// if _, podFind := res.Pods[podRef]; podFind {
			// 	klog.ErrorS(nil, "Got duplicate pod point", "pod", klog.KRef(podRef.Namespace, podRef.Name))
			// 	continue
			// }
			res.Pods[podRef] = podMetricsPoint
		}
	}

	klog.Info("Scrape finished", "duration", myClock.Since(startTime), "nodeCount", len(res.Nodes), "podCount", len(res.Pods))
	return res
}

func (c *scraper) collectNode(ctx context.Context, node *corev1.Node) (*storage.MetricsBatch, error) {
	startTime := myClock.Now()
	defer func() {
		requestDuration.WithLabelValues(node.Name).Observe(float64(myClock.Since(startTime)) / float64(time.Second))
		lastRequestTime.WithLabelValues(node.Name).Set(float64(myClock.Now().Unix()))
	}()
	// if strings.Contains(node.Name, "kind-") {
	ms, err := c.kubeletClient.GetMetrics(ctx, node)
	if err != nil {
		requestTotal.WithLabelValues("false").Inc()
		return nil, err
	}
	requestTotal.WithLabelValues("true").Inc()
	return ms, nil
	// } else {
	// 	makeFakeDelay()
	// 	scrapeTime := time.Now()
	// 	ms := &storage.MetricsBatch{
	// 		Nodes: map[string]storage.MetricsPoint{
	// 			node.Name: fakeMetricPoint(100, 200, scrapeTime),
	// 		},
	// 		Pods: map[apitypes.NamespacedName]storage.PodMetricsPoint{
	// 			{Namespace: "ns1", Name: "pod1"}: {
	// 				Containers: map[string]storage.MetricsPoint{
	// 					"container1":    fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container2":    fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container11":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container21":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container13":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container24":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container15":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container26":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container17":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container28":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container19":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container211":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container112":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container212":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container113":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container243":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container1456": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container752":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container5671": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container82":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 					"container91":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
	// 					"container02":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
	// 				},
	// 			},
	// 			{Namespace: "ns1", Name: "pod2"}: {
	// 				Containers: map[string]storage.MetricsPoint{
	// 					"container1": fakeMetricPoint(700, 800, scrapeTime.Add(30*time.Millisecond)),
	// 				},
	// 			},
	// 			{Namespace: "ns2", Name: "pod1"}: {
	// 				Containers: map[string]storage.MetricsPoint{
	// 					"container1": fakeMetricPoint(900, 1000, scrapeTime.Add(40*time.Millisecond)),
	// 				},
	// 			},
	// 			{Namespace: "ns3", Name: "pod1"}: {
	// 				Containers: map[string]storage.MetricsPoint{
	// 					"container1": fakeMetricPoint(1100, 1200, scrapeTime.Add(50*time.Millisecond)),
	// 				},
	// 			},
	// 		},
	// 	}
	// 	return ms, nil
	// }
}

// for {
// 	klog.Info("%+v\n TTT", node)
// }
// Jake: fake request
// if strings.Contains(node.Name, "kind-") {
// 	// if node.Name == "kind-control-plane" {
// 	// 	_ctx = ctx
// 	// 	_node = node
// 	// }
// ms, err := c.kubeletClient.GetMetrics(ctx, node)
// if err != nil {
// 	requestTotal.WithLabelValues("false").Inc()
// 	return nil, err
// scrapeTime := time.Now()
// ms := &storage.MetricsBatch{
// 	Nodes: map[string]storage.MetricsPoint{
// 		node.Name: fakeMetricPoint(100, 200, scrapeTime),
// 	},
// 	Pods: map[apitypes.NamespacedName]storage.PodMetricsPoint{
// 		{Namespace: "ns1", Name: "pod1"}: {
// 			Containers: map[string]storage.MetricsPoint{
// 				"container1":    fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container2":    fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container11":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container21":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container13":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container24":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container15":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container26":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container17":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container28":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container19":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container211":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container112":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container212":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container113":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container243":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container1456": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container752":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container5671": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container82":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				"container91":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 				"container02":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 			},
// 		},
// 		{Namespace: "ns1", Name: "pod2"}: {
// 			Containers: map[string]storage.MetricsPoint{
// 				"container1": fakeMetricPoint(700, 800, scrapeTime.Add(30*time.Millisecond)),
// 			},
// 		},
// 		{Namespace: "ns2", Name: "pod1"}: {
// 			Containers: map[string]storage.MetricsPoint{
// 				"container1": fakeMetricPoint(900, 1000, scrapeTime.Add(40*time.Millisecond)),
// 			},
// 		},
// 		{Namespace: "ns3", Name: "pod1"}: {
// 			Containers: map[string]storage.MetricsPoint{
// 				"container1": fakeMetricPoint(1100, 1200, scrapeTime.Add(50*time.Millisecond)),
// 			},
// 		},
// 	},
// }
// // if err != nil {
// // 	requestTotal.WithLabelValues("false").Inc()
// // 	return nil, err
// // }
// // requestTotal.WithLabelValues("true").Inc()
// return ms, nil
// }
// requestTotal.WithLabelValues("true").Inc()
// return ms, nil
// } else {
// 	r := rand.Intn(1000)
// 	time.Sleep(time.Duration(r) * time.Millisecond) //working
// 	scrapeTime := time.Now()
// 	ms := &storage.MetricsBatch{
// 		Nodes: map[string]storage.MetricsPoint{
// 			node.Name: fakeMetricPoint(100, 200, scrapeTime),
// 		},
// 		Pods: map[apitypes.NamespacedName]storage.PodMetricsPoint{
// 			{Namespace: "ns1", Name: "pod1"}: {
// 				Containers: map[string]storage.MetricsPoint{
// 					"container1":    fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container2":    fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container11":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container21":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container13":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container24":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container15":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container26":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container17":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container28":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container19":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container211":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container112":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container212":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container113":  fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container243":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container1456": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container752":  fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container5671": fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container82":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 					"container91":   fakeMetricPoint(300, 400, scrapeTime.Add(10*time.Millisecond)),
// 					"container02":   fakeMetricPoint(500, 600, scrapeTime.Add(20*time.Millisecond)),
// 				},
// 			},
// 			{Namespace: "ns1", Name: "pod2"}: {
// 				Containers: map[string]storage.MetricsPoint{
// 					"container1": fakeMetricPoint(700, 800, scrapeTime.Add(30*time.Millisecond)),
// 				},
// 			},
// 			{Namespace: "ns2", Name: "pod1"}: {
// 				Containers: map[string]storage.MetricsPoint{
// 					"container1": fakeMetricPoint(900, 1000, scrapeTime.Add(40*time.Millisecond)),
// 				},
// 			},
// 			{Namespace: "ns3", Name: "pod1"}: {
// 				Containers: map[string]storage.MetricsPoint{
// 					"container1": fakeMetricPoint(1100, 1200, scrapeTime.Add(50*time.Millisecond)),
// 				},
// 			},
// 		},
// 	}
// 	// if err != nil {
// 	// 	requestTotal.WithLabelValues("false").Inc()
// 	// 	return nil, err
// 	// }
// 	// requestTotal.WithLabelValues("true").Inc()
// 	return ms, nil
// }
// }

func makeRandomStr() string {
	// charset use random string
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

	// stringWithCharset return of random string
	b := make([]byte, 10)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

func makeFakeNodes(cnt int) []*corev1.Node {
	var nodes []*corev1.Node
	for i := 0; i < cnt; i++ {
		name := makeRandomStr()
		nodes = append(nodes, makeFakeNode(name, "node.somedomain", "10.0.1.2", true))
	}
	return nodes
}

func makeFakeNode(name, hostName, addr string, ready bool) *corev1.Node {
	res := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady},
			},
		},
	}
	if hostName != "" {
		res.Status.Addresses = append(res.Status.Addresses, corev1.NodeAddress{Type: corev1.NodeHostName, Address: hostName})
	}
	if addr != "" {
		res.Status.Addresses = append(res.Status.Addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: addr})
	}
	if ready {
		res.Status.Conditions[0].Status = corev1.ConditionTrue
	} else {
		res.Status.Conditions[0].Status = corev1.ConditionFalse
	}
	return res
}

func fakeMetricPoint(cpu, memory uint64, time time.Time) storage.MetricsPoint {
	return storage.MetricsPoint{
		Timestamp:         time,
		CumulativeCpuUsed: cpu,
		MemoryUsage:       memory,
	}
}

var dummyvalue int = 0

func makeFakeDelay() int {
	// start := time.Now()
	// rand.Seed(time.Now().UnixNano())
	// r := rand.Intn(1000)
	// r := 30
	// for {
	// 	dummyvalue = dummyvalue + 1
	// 	rand.Intn(100)
	// 	elapsed := time.Now().Nanosecond() - start.Nanosecond()
	// 	if elapsed > r*1000 {
	// 		break
	// 	}
	// }
	// time.Sleep(1000 * time.Millisecond)
	a := 0
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond) // Simulating some delay
		a += rand.Intn(1000) * rand.Intn(1000)
	}
	return rand.Intn(1000) * rand.Intn(1000)
}

type clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(d time.Time) time.Duration { return time.Since(d) }

var myClock clock = &realClock{}

// package main

// import (
// 	"fmt"
// 	"math/rand"
// 	"runtime"
// 	"sync"
// 	"time"
// )

// func main() {
// 	runtime.GOMAXPROCS(runtime.NumCPU())

// 	var wg sync.WaitGroup
// 	numGoroutines := 10000
// 	numCalculations := 100000

// 	fmt.Printf("Starting stress test with %d goroutines, each performing %d calculations\n", numGoroutines, numCalculations)

// 	for i := 0; i < numGoroutines; i++ {
// 		wg.Add(1)
// 		go func() {
// 			defer wg.Done()
// 			performCalculations(numCalculations)
// 		}()
// 	}

// 	wg.Wait()
// 	fmt.Println("Stress test completed")
// }

// func performCalculations(numCalculations int) {
// 	for i := 0; i < numCalculations; i++ {
// 		_ = performHeavyComputation()
// 	}
// }

// func performHeavyComputation() int {
// 	time.Sleep(time.Millisecond) // Simulating some delay
// 	return rand.Intn(1000) * rand.Intn(1000)
// }
