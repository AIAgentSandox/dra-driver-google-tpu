/*
 * Copyright The Kubernetes Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"
)

const RootDirectory = "/"

var (
	networkSettings = []SystemSetting{
		{FilePath: "proc/sys/net/ipv4/tcp_slow_start_after_idle", Value: "0"},
		{FilePath: "proc/sys/net/ipv4/tcp_no_metrics_save", Value: "1"},
		{FilePath: "sys/module/tcp_cubic/parameters/hystart_detect", Value: "2"},
		{FilePath: "proc/sys/net/core/somaxconn", Value: "4096"},
		{FilePath: "proc/sys/net/ipv4/tcp_max_syn_backlog", Value: "4096"},
		{FilePath: "proc/sys/net/ipv4/tcp_mtu_probing", Value: "0"},
		{FilePath: "proc/sys/net/core/optmem_max", Value: "131072"},
	}
)

// SystemSetting contains filePath and its setting value.
type SystemSetting struct {
	FilePath string
	Value    string
}

// ApplyNetworkSettings iterates through a predefined list of network settings
// and applies them by writing to the corresponding system file. After writing,
// it reads back the value and logs it for visibility. The read-back is
// best-effort: the kernel may clamp or normalize a sysctl-style value while
// still accepting the write, so a mismatch is logged but not treated as an error.
// It traverses the entire list before returning, even if an error is encountered.
func ApplyNetworkSettings() error {
	return applyNetworkSettings(RootDirectory)
}

// An implementation for ApplyNetworkSettings but taking parent directory for unit test purpose.
func applyNetworkSettings(parentDir string) error {
	var errs []string
	for _, setting := range networkSettings {
		filePath := filepath.Join(parentDir, setting.FilePath)
		err := os.WriteFile(filePath, []byte(setting.Value), 0644)
		if err != nil {
			klog.Errorf("Error writing to %s: %v", filePath, err)
			errs = append(errs, filePath)
			continue
		}
		// The read-back is best-effort and only serves to warn, so a read
		// failure does not fail the operation once the write has succeeded.
		value, err := os.ReadFile(filePath)
		if err != nil {
			klog.Warningf("Error reading back %s: %v", filePath, err)
			continue
		}
		// Best-effort read-back: the kernel can clamp or normalize a sysctl-style
		// value while still accepting the write, so a mismatch is logged for
		// visibility but not treated as an error. Reads from /proc/sys carry a
		// trailing newline, hence the trim.
		got := strings.TrimSpace(string(value))
		if got != setting.Value {
			klog.Warningf("Value mismatch for %s: wrote %q, read back %q", filePath, setting.Value, got)
		}
		klog.Infof("Current value of %s: %s", filePath, got)
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}
