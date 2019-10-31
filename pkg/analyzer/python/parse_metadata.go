package python

import (
	"bufio"
	"bytes"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/stackrox/rox/pkg/stringutils"
	"github.com/stackrox/scanner/pkg/component"
)

// The metadata file format is specified at https://packaging.python.org/specifications/core-metadata/.
// Note that it's possible that the file is not a Python manifest but some other totally random file that
// happens to have a matching name.
// In this case, this function will gracefully return `nil`.
func parseMetadataFile(filePath string, contents []byte) *component.Component {
	var c *component.Component

	ensureCInitialized := func() {
		if c == nil {
			c = &component.Component{
				Location:   filePath,
				SourceType: component.PythonSourceType,
			}
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		currentLine := scanner.Text()
		key, value := stringutils.Split2(currentLine, ":")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" || key == "" {
			continue
		}
		switch key {
		case "Name":
			ensureCInitialized()
			c.Name = value
		case "Version":
			ensureCInitialized()
			c.Version = value
		}

		// If we have got all the information we want, no point in scanning the rest of the file.
		if c != nil && c.Name != "" && c.Version != "" {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		log.Errorf("Error scanning file %q: %v", filePath, err)
		return nil
	}

	return c
}