package types

import (
	"github.com/stackrox/scanner/database"
)

type MetadataEnricher interface {
	Metadata() interface{}
	Summary() string
}

// AppendFunc is the type of a callback provided to an Appender.
type AppendFunc func(metadataKey string, metadata MetadataEnricher, severity database.Severity)

// Appender represents anything that can fetch vulnerability metadata and
// append it to a Vulnerability.
type Appender interface {
	// BuildCache loads metadata into memory such that it can be quickly accessed
	// for future calls to Append.
	BuildCache(dumpDir string) error

	// AddMetadata adds metadata to the given database.Vulnerability.
	// It is expected that the fetcher uses .Lock.Lock() when manipulating the Metadata map.
	// Append
	Append(name string, subCVEs []string, callback AppendFunc) error

	// PurgeCache deallocates metadata from memory after all calls to Append are
	// finished.
	PurgeCache()

	// Name returns the name of the appender
	Name() string
}