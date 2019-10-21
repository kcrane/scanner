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

// Package nvd implements a vulnerability metadata appender using the NIST NVD
// database.
package nvd

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/vulnmdsrc"
	"github.com/stackrox/scanner/pkg/commonerr"
	"github.com/stackrox/scanner/pkg/httputil"
)

const (
	dataFeedURL     string = "https://nvd.nist.gov/feeds/json/cve/1.1/nvdcve-1.1-%s.json.gz"
	dataFeedMetaURL string = "https://nvd.nist.gov/feeds/json/cve/1.1/nvdcve-1.1-%s.meta"

	appenderName string = "NVD"

	logDataFeedName string = "data feed name"

	maxFetchRetries = 10
)

type appender struct {
	localPath      string
	dataFeedHashes map[string]string
	metadata       map[string]*metadataEnricher
}

type metadataEnricher struct {
	metadata *Metadata
	summary  string
}

func (m *metadataEnricher) Metadata() interface{} {
	return m.metadata
}

func (m *metadataEnricher) Summary() string {
	return m.summary
}

func newMetadataEnricher(nvd *nvdEntry) *metadataEnricher {
	return &metadataEnricher{
		metadata: nvd.Metadata(),
		summary:  nvd.Summary(),
	}
}

type Metadata struct {
	PublishedDateTime    string
	LastModifiedDateTime string
	CVSSv2               NVDmetadataCVSSv2
	CVSSv3               NVDmetadataCVSSv3
}

type NVDmetadataCVSSv2 struct {
	Vectors             string
	Score               float64
	ExploitabilityScore float64
	ImpactScore         float64
}

type NVDmetadataCVSSv3 struct {
	Vectors             string
	Score               float64
	ExploitabilityScore float64
	ImpactScore         float64
}

func init() {
	vulnmdsrc.RegisterAppender(appenderName, &appender{})
}

func (a *appender) BuildCache(datastore database.Datastore) error {
	var err error
	a.metadata = make(map[string]*metadataEnricher)

	// Init if necessary.
	if a.localPath == "" {
		// Create a temporary folder to store the NVD data and create hashes struct.
		if a.localPath, err = ioutil.TempDir(os.TempDir(), "nvd-data"); err != nil {
			return commonerr.ErrFilesystem
		}

		a.dataFeedHashes = make(map[string]string)
	}

	// Get data feeds.
	dataFeedReaders, dataFeedHashes, err := getDataFeeds(a.dataFeedHashes, a.localPath)
	if err != nil {
		return err
	}
	a.dataFeedHashes = dataFeedHashes

	// Parse data feeds.
	for dataFeedName, dataFileName := range dataFeedReaders {
		f, err := os.Open(dataFileName)
		if err != nil {
			log.WithError(err).WithField(logDataFeedName, dataFeedName).Error("could not open NVD data file")
			return commonerr.ErrCouldNotParse
		}

		if err := a.parseDataFeed(bufio.NewReader(f)); err != nil {
			log.WithError(err).WithField(logDataFeedName, dataFeedName).Error("could not parse NVD data file")
			return err
		}
		f.Close()
	}

	return nil
}

func (a *appender) parseDataFeed(r io.Reader) error {
	var nvd nvd

	if err := json.NewDecoder(r).Decode(&nvd); err != nil {
		return commonerr.ErrCouldNotParse
	}

	for _, nvdEntry := range nvd.Entries {
		// Create metadata entry.
		enricher := newMetadataEnricher(&nvdEntry)
		if enricher.metadata != nil {
			a.metadata[nvdEntry.Name()] = enricher
		}
	}

	return nil
}

func (a *appender) getHighestCVSSMetadata(cves []string) *Metadata {
	var maxScore float64
	var maxMetadata *Metadata
	for _, cve := range cves {
		if enricher, ok := a.metadata[cve]; ok {
			nvdMetadata := enricher.metadata
			if nvdMetadata.CVSSv3.Score != 0 && nvdMetadata.CVSSv3.Score > maxScore {
				maxScore = nvdMetadata.CVSSv3.Score
				maxMetadata = nvdMetadata
			} else if nvdMetadata.CVSSv2.Score > maxScore {
				maxScore = nvdMetadata.CVSSv2.Score
				maxMetadata = nvdMetadata
			}
		}
	}

	return maxMetadata
}

func (a *appender) Append(name string, subCVEs []string, appendFunc vulnmdsrc.AppendFunc) error {
	if enricher, ok := a.metadata[name]; ok {
		appendFunc(appenderName, enricher, SeverityFromCVSS(enricher.metadata))
		return nil
	}
	if nvdMetadata := a.getHighestCVSSMetadata(subCVEs); nvdMetadata != nil {
		appendFunc(appenderName, &metadataEnricher{metadata: nvdMetadata}, SeverityFromCVSS(nvdMetadata))
	}
	return nil
}

func (a *appender) PurgeCache() {
	a.metadata = nil
}

func (a *appender) Clean() {
	os.RemoveAll(a.localPath)
}

func getDataFeeds(dataFeedHashes map[string]string, localPath string) (map[string]string, map[string]string, error) {
	var dataFeedNames []string
	for y := 2002; y <= time.Now().Year(); y++ {
		dataFeedNames = append(dataFeedNames, strconv.Itoa(y))
	}

	// Get hashes for these feeds.
	for _, dataFeedName := range dataFeedNames {
		hash, err := getHashFromMetaURL(fmt.Sprintf(dataFeedMetaURL, dataFeedName))
		if err != nil {
			log.WithError(err).WithField(logDataFeedName, dataFeedName).Warning("could not get NVD data feed hash")

			// It's not a big deal, no need interrupt, we're just going to download it again then.
			continue
		}

		dataFeedHashes[dataFeedName] = hash
	}

	// Create map containing the name and filename for every data feed.
	dataFeedReaders := make(map[string]string)
	for _, dataFeedName := range dataFeedNames {
		fileName := filepath.Join(localPath, fmt.Sprintf("%s.json", dataFeedName))

		if h, ok := dataFeedHashes[dataFeedName]; ok && h == dataFeedHashes[dataFeedName] {
			// The hash is known, the disk should contains the feed. Try to read from it.
			if localPath != "" {
				if _, err := os.Stat(fileName); err == nil {
					dataFeedReaders[dataFeedName] = fileName
					continue
				}
			}

			err := downloadFeed(dataFeedName, fileName)
			if err != nil {
				return dataFeedReaders, dataFeedHashes, err
			}
			dataFeedReaders[dataFeedName] = fileName
		}
	}

	return dataFeedReaders, dataFeedHashes, nil
}

func getWithRetriesOnForbidden(url string) (*http.Response, error) {
	var (
		resp  *http.Response
		err   error
		count int
	)

	for count < maxFetchRetries {
		if count != 0 {
			time.Sleep(1 * time.Second)
		}

		resp, err = httputil.GetWithUserAgent(url)
		if err != nil {
			log.WithError(err).WithField("NVD URL", url).Error("could not download feed")
			return nil, commonerr.ErrCouldNotDownload
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		_ = resp.Body.Close()
		count++
	}
	if resp == nil {
		return nil, fmt.Errorf("could not get URL %q after retries", url)
	}
	return resp, nil
}

func downloadFeed(dataFeedName, fileName string) error {
	// Download data feed.

	newDataFeedURL := fmt.Sprintf(dataFeedURL, dataFeedName)
	r, err := getWithRetriesOnForbidden(newDataFeedURL)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	// Un-gzip it.
	gr, err := gzip.NewReader(r.Body)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"StatusCode": r.StatusCode, "DataFeedName": dataFeedName}).Error("could not read NVD data feed")
		return commonerr.ErrCouldNotDownload
	}

	// Store it to a file at the same time if possible.
	f, err := os.Create(fileName)
	if err != nil {
		log.WithError(err).WithField("Filename", fileName).Warning("could not store NVD data feed to filesystem")
		return commonerr.ErrFilesystem
	}
	defer f.Close()

	_, err = io.Copy(f, gr)
	if err != nil {
		log.WithError(err).WithField("Filename", fileName).Warning("could not stream NVD data feed to filesystem")
		return commonerr.ErrFilesystem
	}

	return nil
}

func getHashFromMetaURL(metaURL string) (string, error) {
	r, err := httputil.GetWithUserAgent(metaURL)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()

	if !httputil.Status2xx(r) {
		return "", fmt.Errorf("%s failed status: %s", metaURL, r.Status)
	}

	scanner := bufio.NewScanner(r.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "sha256:") {
			return strings.TrimPrefix(line, "sha256:"), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", errors.New("invalid .meta file format")
}

// SeverityFromCVSS converts the CVSS Score (0.0 - 10.0) into a
// database.Severity following the qualitative rating scale available in the
// CVSS v3.0 specification (https://www.first.org/cvss/specification-document),
// Table 14.
//
// The Negligible level is set for CVSS scores between [0, 1), replacing the
// specified None level, originally used for a score of 0.
func SeverityFromCVSS(meta *Metadata) database.Severity {
	score := meta.CVSSv3.Score
	if score == 0 {
		score = meta.CVSSv2.Score
	}
	switch {
	case score < 1.0:
		return database.NegligibleSeverity
	case score < 3.9:
		return database.LowSeverity
	case score < 6.9:
		return database.MediumSeverity
	case score < 8.9:
		return database.HighSeverity
	case score <= 10:
		return database.CriticalSeverity
	}
	return database.UnknownSeverity
}
