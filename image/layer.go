// Copyright 2016 The Linux Foundation
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

package image

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/opencontainers/image-tools/utils"
)

func CreateLayer(child, parent, dest string) error {
	arch, err := Diff(child, parent)
	if err != nil {
		return err
	}
	defer arch.Close()
	filename := fmt.Sprintf("%s.tar", filepath.Clean(child))
	if dest != "" {
		filename = filepath.Clean(dest)
	}
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, arch)
	return err
}

// Diff produces an archive of the changes between the specified
// layer and its parent layer which may be "".
func Diff(child, parent string) (arch io.ReadCloser, err error) {
	changes, err := ChangesDirs(child, parent)
	if err != nil {
		return nil, err
	}
	archive, err := exportChanges(child, changes)
	if err != nil {
		return nil, err
	}
	return archive, nil
}

// ExportChanges produces an Archive from the provided changes, relative to dir.
func exportChanges(dir string, changes []Change) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	go func() {
		ta := &utils.TarAppender{
			TarWriter: tar.NewWriter(writer),
			Buffer:    utils.BufioWriter32KPool.Get(nil),
			SeenFiles: make(map[uint64]string),
		}
		// this buffer is needed for the duration of this piped stream
		defer utils.BufioWriter32KPool.Put(ta.Buffer)

		sort.Sort(changesByPath(changes))

		// In general we log errors here but ignore them because
		// during e.g. a diff operation the container can continue
		// mutating the filesystem and we can see transient errors
		// from this
		for _, change := range changes {
			if change.Kind == ChangeDelete {
				whiteOutDir := filepath.Dir(change.Path)
				whiteOutBase := filepath.Base(change.Path)
				whiteOut := filepath.Join(whiteOutDir, ".wh."+whiteOutBase)
				timestamp := time.Now()
				hdr := &tar.Header{
					Name:       whiteOut[1:],
					Size:       0,
					ModTime:    timestamp,
					AccessTime: timestamp,
					ChangeTime: timestamp,
				}
				if err := ta.TarWriter.WriteHeader(hdr); err != nil {
					logrus.Debugf("Can't write whiteout header: %s", err)
				}
			} else {
				path := filepath.Join(dir, change.Path)
				if err := ta.AddTarFile(path, change.Path[1:]); err != nil {
					logrus.Debugf("Can't add file %s to tar: %s", path, err)
				}
			}
		}

		// Make sure to check the error on Close.
		if err := ta.TarWriter.Close(); err != nil {
			logrus.Debugf("Can't close layer: %s", err)
		}
		if err := writer.Close(); err != nil {
			logrus.Debugf("failed close Changes writer: %s", err)
		}
	}()
	return reader, nil
}
