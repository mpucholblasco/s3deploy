// Copyright © 2018 Bjørn Erik Pedersen <bjorn.erik.pedersen@gmail.com>.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lib

import (
	"errors"
	"fmt"
	"io/ioutil"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSFile(t *testing.T) {
	assert := require.New(t)

	of, err := openTestFile("main.css")
	assert.NoError(err)

	assert.Equal(int64(3), of.Size())
	assert.Equal(`"902fbdd2b1df0c4f70b4a5d23525e932"`, of.ETag())
	assert.NotNil(of.Content())
	b, err := ioutil.ReadAll(of.Content())
	assert.NoError(err)
	assert.Equal("ABC", string(b))
	assert.Equal("text/css; charset=utf-8", of.Headers()["Content-Type"])
}

func TestShouldThisReplace(t *testing.T) {
	assert := require.New(t)

	of, err := openTestFile("main.css")
	assert.NoError(err)

	correctETag := `"902fbdd2b1df0c4f70b4a5d23525e932"`

	for i, test := range []struct {
		testFile
		expect       bool
		expectReason string
	}{
		{testFile{"k1", int64(123), correctETag}, true, "size"},
		{testFile{"k2", int64(3), "FOO"}, true, "ETag"},
		{testFile{"k3", int64(3), correctETag}, false, ""},
	} {
		message := fmt.Sprintf("Test %d", i)
		b, reason := of.shouldThisReplace(test.testFile)
		assert.Equal(test.expect, b, message)
		assert.Equal(uploadReason(test.expectReason), reason)
	}
}

func TestDetectContentTypeFromContent(t *testing.T) {
	assert := require.New(t)

	assert.Equal("text/html; charset=utf-8", detectContentTypeFromContent([]byte("<html>foo</html>")))
	assert.Equal("text/html; charset=utf-8", detectContentTypeFromContent([]byte("<html>"+strings.Repeat("abc", 300)+"</html>")))
}

type testFile struct {
	key  string
	size int64
	etag string
}

func (f testFile) Key() string {
	return f.key
}

func (f testFile) ETag() string {
	return f.etag
}

func (f testFile) Size() int64 {
	return f.size
}

func openTestFile(name string) (*osFile, error) {
	rootPath := "/mylocalstore"
	absName := path.Join(rootPath, name)
	s := newTestLocalStore(rootPath,
		newTestLocalFile(".s3deploy.yml", []byte("my test")),
		newTestLocalFile("index.html", []byte("<html>s3deploy</html>\n")),
		newTestLocalFile("ab.txt", []byte("AB\n")),
		newTestLocalFile("main.css", []byte("ABC")),
	)

	f, ok := s.files[absName]
	if !ok {
		return nil, errors.New("Error opening file")
	}
	tmpFile := newTmpFile(f.relPath, absName, f.Size())

	return newOSFile(s, nil, "", tmpFile)
}
