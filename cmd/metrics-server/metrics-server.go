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

package main

import (
	"fmt"
	"os"
	"runtime"

	genericapiserver "k8s.io/apiserver/pkg/server"

	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"
	"sigs.k8s.io/metrics-server/cmd/metrics-server/app"
)

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	klog.Info("TEST!!!!")
	klog.ErrorS(nil, "TEST!!!!")
	fmt.Println("This is your custom log message")
	if len(os.Getenv("GOMAXPROCS")) == 0 {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	runtime.GOMAXPROCS(runtime.NumCPU())
	// fmt.Println(runtime.GOMAXPROCS(0))
	// a := 0
	// for i := 0; i < 20; i++ {
	// 	go func(a int) {
	// 		rand.Seed(time.Now().UnixNano())
	// 		for {
	// 			a = a + 1
	// 			// println(rand.Intn(100))
	// 		}
	// 	}(a)
	// }
	// for {
	// 	// fmt.Println("This is your custom log message")
	// 	a = a + 1
	// }
	cmd := app.NewMetricsServerCommand(genericapiserver.SetupSignalHandler())
	if err := cmd.Execute(); err != nil {
		panic(err)
	}
}
