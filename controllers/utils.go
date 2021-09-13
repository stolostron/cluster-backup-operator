/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"fmt"
	"time"
)

// find takes a slice and looks for an element in it. If found it will
// return it's key, otherwise it will return -1 and a bool of false.
func find(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}

//append unique value to a list
func appendUnique(slice []string, value string) []string {

	// check if the NS exists
	_, ok := find(slice, value)
	if !ok {
		slice = append(slice, value)
	}
	return slice
}

// return current time formatted to validate k8s names
func getFormattedTimeCRD(t time.Time) string {
	formatted := fmt.Sprintf("%d-%02d-%02d-%02d%02d%02d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second())
	return formatted
}

// return Duration in format 1h15m30s
func getFormattedDuration(duration time.Duration) string {

	formatted := duration.Truncate(time.Second).String()
	return formatted
}

// name used by the velero backup resource, created by the backup acm controller
func getVeleroBackupName(backupName, backupNamesapce string) string {
	return backupName + "-" + getFormattedTimeCRD(time.Now())
}

// min returns the smallest of x or y.
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}
