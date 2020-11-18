// Copyright (c) 2020 Doc.ai and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package k8s provides kubernetes helper functions
package k8s

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/edwarnicke/exechelper"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

var once sync.Once
var client *kubernetes.Clientset
var clientErr error

const namespace = "default"

// Client returns k8s client
func Client() (*kubernetes.Clientset, error) {
	once.Do(func() {
		path := os.Getenv("KUBECONFIG")
		if path == "" {
			path = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		config, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			clientErr = err
			return
		}
		client, clientErr = kubernetes.NewForConfig(config)
	})
	return client, clientErr
}

// Nodes returns a slice of Nodes where can be deployed deployment
func Nodes() ([]*corev1.Node, error) {
	c, err := Client()
	if err != nil {
		return nil, err
	}
	response, err := c.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []*corev1.Node
	for i := 0; i < len(response.Items); i++ {
		node := &response.Items[i]
		name := node.Labels["kubernetes.io/hostname"]
		if !strings.HasSuffix(name, "control-plane") {
			result = append(result, node)
		}
	}

	return result, nil
}

// ApplyDeployment is analogy of 'kubeclt apply -f path' but with mutating deployment before apply
func ApplyDeployment(path string, mutators ...func(deployment *v1.Deployment)) error {
	client, err := Client()
	if err != nil {
		return err
	}
	b, err := ioutil.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	var d v1.Deployment
	if parseErr := yaml.Unmarshal(b, &d); parseErr != nil {
		return parseErr
	}
	for _, m := range mutators {
		m(&d)
	}
	_, err = client.AppsV1().Deployments(namespace).Create(&d)
	return err
}

// SetNode sets NodeSelector for the pod based on passed nodeName
func SetNode(nodeName string) func(*v1.Deployment) {
	return func(deployment *v1.Deployment) {
		deployment.Spec.Template.Spec.NodeSelector = map[string]string{
			"kubernetes.io/hostname": nodeName,
		}
	}
}

// WaitLogsMatch waits pattern in logs of deployment. Note: Use this function only for final assertion.
// Do not use this for wait special state of application.
// Note: This API should be replaced to using `ping` command.
func WaitLogsMatch(labelSelector, pattern string, timeout time.Duration) error {
	start := time.Now()
	for {
		sb := new(strings.Builder)
		err := exechelper.Run(fmt.Sprintf("kubectl logs -l %v", labelSelector), exechelper.WithStderr(sb), exechelper.WithStdout(sb))
		if err != nil {
			return err
		}
		logs := sb.String()
		if ok, err := regexp.MatchString(pattern, logs); err != nil {
			return err
		} else if ok {
			return nil
		}
		if time.Since(start) >= timeout {
			return errors.New("timeout for wait pattern: " + pattern)
		}
		time.Sleep(time.Millisecond * 100)
	}
}

// ShowLogs prints logs into console all containers of pods
func ShowLogs(options ...*exechelper.Option) {
	client, err := Client()

	if err != nil {
		logrus.Errorf("Cannot get k8s client: %v", err.Error())
		return
	}

	pods, err := client.CoreV1().Pods(namespace).List(metav1.ListOptions{})

	if err != nil {
		logrus.Errorf("Cannot get pods: %v", err.Error())
		return
	}

	for i := 0; i < len(pods.Items); i++ {
		pod := &pods.Items[i]
		for j := 0; j < len(pod.Spec.Containers); j++ {
			container := &pod.Spec.Containers[j]
			_ = exechelper.Run(fmt.Sprintf("kubectl logs %v -c %v ", pod.Name, container.Name), options...)
		}
	}
}
