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

import "strings"

func findSuffix(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if strings.HasSuffix(val, item) {
			return i, true
		}
	}
	return -1, false
}

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

// min returns the smallest of x or y.
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// returns a valid name for the velero restore kubernetes resource
// by trimming the concatenated cluster restore and backup names
func getValidKsRestoreName(clusterRestoreName string, backupName string) string {
	//max name for ns or resources is 253 chars
	fullName := clusterRestoreName + "-" + backupName

	if len(fullName) > 252 {
		return fullName[:252]
	}
	return fullName
}
