/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package service

import (
	"reflect"
	"regexp"
	"sort"

	log "github.com/sirupsen/logrus"
)

// stringSlicesEqual returns true IFF two slices contain same members
// The input slices are sorted
func stringSlicesEqual(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)

	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// stringSliceRegexMatcher returns an updated slice with members from the original slice that matched the regexpPattern
// The input slice is not changed
func stringSliceRegexMatcher(slice []string, regexpPattern string) []string {
	//log.Debug("Regex pattern %s slice: %s\n", regexpPattern, slice)
	out := make([]string, 0)
	for _, str := range slice {
		matched, err := regexp.MatchString(regexpPattern, str)
		if err != nil {
			log.Error("Regex: " + regexpPattern + " error: " + err.Error())
			break
		}
		if matched {
			out = append(out, str)
		}
	}
	//log.Debug("Regex out: %s\n", out)
	return out
}

// stringSliceReplaceAll returns an updated slice with the indicated regex replacement made.
// The input slice is changed in place
func stringSliceRegexReplace(slice []string, regexpPattern string, replacement string) {
	re := regexp.MustCompile(regexpPattern)
	for i := range slice {
		slice[i] = re.ReplaceAllString(slice[i], replacement)
	}
	//log.Debug("ReplaceAll slice: %s\n", slice)
}

// AppendIfMissing - Appends a string to a slice if not already present
// in slice
func appendIfMissing(slice []string, str string) []string {
	for _, ele := range slice {
		if ele == str {
			return slice
		}
	}
	return append(slice, str)
}
