// Copyright 2017 clair authors
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

// Package notification fetches notifications from the database and informs the
// specified remote handler about their existences, inviting the third party to
// actively query the API about it.

// Package imagefmt exposes functions to dynamically register methods to
// detect different types of container image formats.
package imagefmt

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/stackrox/rox/pkg/utils"
	"github.com/stackrox/scanner/pkg/commonerr"
	"github.com/stackrox/scanner/pkg/matcher"
	"github.com/stackrox/scanner/pkg/tarutil"
)

var (
	extractorsM sync.RWMutex
	extractors  = make(map[string]Extractor)
)

// Extractor represents an ability to extract files from a particular container
// image format.
type Extractor interface {
	// ExtractFiles produces a tarutil.LayerFiles from a image layer.
	ExtractFiles(layer io.ReadCloser, filenameMatcher matcher.Matcher) (tarutil.LayerFiles, error)
}

// RegisterExtractor makes an extractor available by the provided name.
//
// If called twice with the same name, the name is blank, or if the provided
// Extractor is nil, this function panics.
func RegisterExtractor(name string, d Extractor) {
	extractorsM.Lock()
	defer extractorsM.Unlock()

	if name == "" {
		panic("imagefmt: could not register an Extractor with an empty name")
	}

	if d == nil {
		panic("imagefmt: could not register a nil Extractor")
	}

	// Enforce lowercase names, so that they can be reliably be found in a map.
	name = strings.ToLower(name)

	if _, dup := extractors[name]; dup {
		panic("imagefmt: RegisterExtractor called twice for " + name)
	}

	extractors[name] = d
}

// Extractors returns the list of the registered extractors.
func Extractors() map[string]Extractor {
	extractorsM.RLock()
	defer extractorsM.RUnlock()

	ret := make(map[string]Extractor)
	for k, v := range extractors {
		ret[k] = v
	}

	return ret
}

// ExtractFromReader extracts the files from a reader which is in the format of a .tar.gz
func ExtractFromReader(reader io.ReadCloser, format string, filenameMatcher matcher.Matcher) (*tarutil.LayerFiles, error) {
	defer utils.IgnoreError(reader.Close)

	if extractor, exists := Extractors()[strings.ToLower(format)]; exists {
		files, err := extractor.ExtractFiles(reader, filenameMatcher)
		if err != nil {
			return nil, err
		}
		return &files, nil
	}

	return nil, commonerr.NewBadRequestError(fmt.Sprintf("unsupported image format %q", format))
}
