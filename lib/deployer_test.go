// Copyright © 2018 Bjørn Erik Pedersen <bjorn.erik.pedersen@gmail.com>.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lib

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	_ remoteStore = (*testRemoteStore)(nil)
)

func TestDeploy(t *testing.T) {
	assert := require.New(t)
	store, m := newTestRemoteStore(0, "")

	d := newTestDeployer(newTestDefaultFileConfig(), newTestDefaultLocalStoreSample(), store)
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.NoError(err)
	assert.Equal("Deleted 1 of 1, uploaded 3, skipped 1 (80% changed)", stats.Summary())
	assertKeys(t, m, ".s3deploy.yml", "main.css", "index.html", "ab.txt")

	mainCSS := m["main.css"]
	assert.IsType(&osFile{}, mainCSS)
	headers := mainCSS.(*osFile).Headers()
	assert.Equal("gzip", headers["Content-Encoding"])
	assert.Equal("text/css; charset=utf-8", headers["Content-Type"])
	assert.Equal("max-age=630720000, no-transform, public", headers["Cache-Control"])
}

func TestDeployWithBucketPath(t *testing.T) {
	assert := require.New(t)
	root := "my/path"
	store, m := newTestRemoteStore(0, root)

	ls := newTestLocalStore("/mylocalstore",
		newTestLocalFile("my/path/.s3deploy.yml", []byte("my test")),
		newTestLocalFile("my/path/index.html", []byte("<html>s3deploy</html>\n")),
		newTestLocalFile("my/path/ab.txt", []byte("AB\n")),
		newTestLocalFile("my/path/main.css", []byte("ABC")),
	)

	d := &Deployer{
		cfg: &Config{
			BucketName:    "example.com",
			RegionName:    "eu-west-1",
			MaxDelete:     300,
			PublicReadACL: true,
			Silent:        true,
			SourcePath:    "/mylocalstore",
			conf: fileConfig{
				Routes: []*route{
					&route{
						Route: "^.+\\.(js|css|svg|ttf)$",
						Headers: map[string]string{
							"Cache-Control": "max-age=630720000, no-transform, public",
						},
						Gzip: true,
					},
					&route{
						Route: "^.+\\.(png|jpg)$",
						Headers: map[string]string{
							"Cache-Control": "max-age=630720000, no-transform, public",
						},
						Gzip: true,
					},
					&route{
						Route: "^.+\\.(html|xml|json)$",
						Gzip:  true,
					},
				},
			},
		},
		outv:    ioutil.Discard,
		printer: newPrinter(ioutil.Discard),
		stats:   &DeployStats{},
		local:   ls,
	}
	d.store = newStore(*d.cfg, store)
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())

	assert.NoError(err)
	assert.Equal("Deleted 1 of 1, uploaded 3, skipped 1 (80% changed)", stats.Summary())
	assertKeys(t, m, "my/path/.s3deploy.yml", "my/path/main.css", "my/path/index.html", "my/path/ab.txt")
	mainCss := m["my/path/main.css"]
	assert.IsType(&osFile{}, mainCss)
	assert.Equal("my/path/main.css", mainCss.(*osFile).Key())
	headers := mainCss.(*osFile).Headers()
	assert.Equal("gzip", headers["Content-Encoding"])

}

func TestDeployOrder(t *testing.T) {
	assert := require.New(t)
	store, m := newTestRemoteStore(0, "")
	store.delayMillis = 5

	d := newTestDeployer(newTestFileConfigWithOrder(), newTestDefaultLocalStoreSample(), store)
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.NoError(err)
	assert.Equal("Deleted 1 of 1, uploaded 3, skipped 1 (80% changed)", stats.Summary())

	indexHTML := m["index.html"]
	assert.IsType(&osFile{}, indexHTML)
	headers := indexHTML.(*osFile).Headers()
	assert.Equal("gzip", headers["Content-Encoding"])
	assert.Equal("text/html; charset=utf-8", headers["Content-Type"])

	// index.html must be the last one on being uploaded
	indexStart := store.timeActionPut["index.html"].start
	assertStartsAfterKeys(t, indexStart, store.timeActionPut, ".s3deploy.yml", "main.css")
}

func TestDeployOrderErrorOpeningFile(t *testing.T) {
	assert := require.New(t)
	store, _ := newTestRemoteStore(0, "")
	store.delayMillis = 5

	ls := newTestLocalStore("/mylocalstore",
		newTestLocalFile(".s3deploy.yml", []byte("my test")),
		newTestLocalFile("index.html", []byte("<html>s3deploy</html>\n")),
		newTestLocalFile("ab.txt", []byte("AB\n")),
		newTestLocalFile("main.css", []byte("ABC")).withErrorOpening(true),
	)

	d := newTestDeployer(newTestFileConfigWithOrder(), ls, store)
	assert.NoError(d.cfg.conf.CompileResources())

	_, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.Error(err)
	assert.Contains(err.Error(), "Error opening file")

	assert.Equal(time.Time{}, store.timeActionPut["index.html"].start, "index.html must not be uploaded")
}

func TestDeleteAfterAllUploadsOnDeploy(t *testing.T) {
	assert := require.New(t)
	store, m := newTestRemoteStore(0, "")

	d := newTestDeployer(newTestDefaultFileConfig(), newTestDefaultLocalStoreSample(), store)
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.NoError(err)
	assert.Equal("Deleted 1 of 1, uploaded 3, skipped 1 (80% changed)", stats.Summary())
	assertKeys(t, m, ".s3deploy.yml", "main.css", "index.html", "ab.txt")

	deleteStart := store.timeActionDelete["deleteme.txt"].start
	assertStartsAfterKeys(t, deleteStart, store.timeActionPut, ".s3deploy.yml", "main.css", "index.html")
}

func TestGroupLocalFiles(t *testing.T) {
	assert := require.New(t)

	d := newTestDeployer(newTestFileConfigWithOrder(), newTestDefaultLocalStoreSample(), nil)
	assert.NoError(d.cfg.conf.CompileResources())

	localFilesGroupped, err := d.groupLocalFiles(context.Background(), d.local, "/mylocalstore")
	assert.NoError(err)
	expectedGroup := [][]string{
		[]string{"ab.txt", ".s3deploy.yml", "main.css"},
		[]string{},
		[]string{"index.html"},
	}
	assertGroup(t, expectedGroup, localFilesGroupped)
}

func TestDeployForce(t *testing.T) {
	assert := require.New(t)
	store, _ := newTestRemoteStore(0, "")

	d := newTestDeployer(newTestDefaultFileConfig(), newTestDefaultLocalStoreSample(), store)
	d.cfg.Force = true
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.NoError(err)
	assert.Equal("Deleted 1 of 1, uploaded 4, skipped 0 (100% changed)", stats.Summary())
}

func TestDeploySourceNotFound(t *testing.T) {
	assert := require.New(t)
	store, _ := newTestRemoteStore(0, "")
	wd, _ := os.Getwd()
	source := filepath.Join(wd, "thisdoesnotexist")

	cfg := &Config{
		BucketName: "example.com",
		RegionName: "eu-west-1",
		MaxDelete:  300,
		Silent:     true,
		SourcePath: source,
		baseStore:  store,
	}

	stats, err := Deploy(cfg)
	assert.Error(err)
	assert.Contains(err.Error(), "thisdoesnotexist")
	assert.Contains(stats.Summary(), "Deleted 0 of 0, uploaded 0, skipped 0")

}

func TestDeployInvalidSourcePath(t *testing.T) {
	assert := require.New(t)
	store, _ := newTestRemoteStore(0, "")
	root := "/"

	if runtime.GOOS == "windows" {
		root = `C:\`
	}

	cfg := &Config{
		BucketName: "example.com",
		RegionName: "eu-west-1",
		MaxDelete:  300,
		Silent:     true,
		SourcePath: root,
		baseStore:  store,
	}

	stats, err := Deploy(cfg)
	assert.Error(err)
	assert.Contains(err.Error(), "invalid source path")
	assert.Contains(stats.Summary(), "Deleted 0 of 0, uploaded 0, skipped 0")

}

func TestDeployNoBucket(t *testing.T) {
	assert := require.New(t)
	_, err := Deploy(&Config{Silent: true})
	assert.Error(err)
}

func TestDeployStoreFailures(t *testing.T) {
	for i := 1; i <= 3; i++ {
		assert := require.New(t)

		store, _ := newTestRemoteStore(i, "")
		message := fmt.Sprintf("Failure %d", i)

		d := newTestDeployer(newTestDefaultFileConfig(), newTestDefaultLocalStoreSample(), store)
		assert.NoError(d.cfg.conf.CompileResources())

		stats, err := d.deploy(context.Background(), runtime.NumCPU())
		assert.Error(err)

		if i == 3 {
			// Fail delete step
			assert.Contains(stats.Summary(), "Deleted 0 of 0, uploaded 3", message)
		} else {
			assert.Contains(stats.Summary(), "Deleted 0 of 0, uploaded 0", message)
		}
	}
}

func TestDeployMaxDelete(t *testing.T) {
	assert := require.New(t)

	m := make(map[string]file)

	for i := 0; i < 200; i++ {
		m[fmt.Sprintf("file%d.css", i)] = &testFile{}
	}

	store := newTestRemoteStoreFrom(m, 0)
	store.delayMillis = 5

	d := newTestDeployer(newTestDefaultFileConfig(), newTestDefaultLocalStoreSample(), store)
	d.cfg.MaxDelete = 42
	assert.NoError(d.cfg.conf.CompileResources())

	stats, err := d.deploy(context.Background(), runtime.NumCPU())
	assert.NoError(err)
	assert.Equal(158+4, len(m))
	assert.Equal("Deleted 42 of 200, uploaded 4, skipped 0 (100% changed)", stats.Summary())

}

func newTestRemoteStore(failAt int, root string) (*testRemoteStore, map[string]file) {
	m := map[string]file{
		path.Join(root, "ab.txt"):       &testFile{key: path.Join(root, "ab.txt"), etag: `"7b0ded95031647702b8bed17dce7698a"`, size: int64(3)},
		path.Join(root, "main.css"):     &testFile{key: path.Join(root, "main.css"), etag: `"changed"`, size: int64(27)},
		path.Join(root, "deleteme.txt"): &testFile{},
	}

	return newTestRemoteStoreFrom(m, failAt), m
}

func newTestRemoteStoreFrom(m map[string]file, failAt int) *testRemoteStore {
	return &testRemoteStore{
		m:                m,
		failAt:           failAt,
		timeActionPut:    make(map[string]timeActions),
		timeActionDelete: make(map[string]timeActions),
	}
}

type timeActions struct {
	start time.Time
	end   time.Time
}

type testRemoteStore struct {
	failAt           int
	delayMillis      time.Duration
	m                map[string]file
	remote           map[string]file
	timeActionPut    map[string]timeActions
	timeActionDelete map[string]timeActions

	sync.Mutex
}

func assertKeys(t *testing.T, m map[string]file, keys ...string) {
	if len(keys) != len(m) {
		t.Log(m)
		t.Fatalf("map length mismatch asserting keys: %d vs %d", len(keys), len(m))
	}

	for _, k := range keys {
		if _, found := m[k]; !found {
			t.Fatal("key not found:", k)
		}
	}
}

func assertStartsAfterKeys(t *testing.T, start time.Time, ta map[string]timeActions, keys ...string) {
	for _, k := range keys {
		if tae, found := ta[k]; !found {
			t.Fatal("time action not found for key:", k)
		} else {
			if start.Before(tae.end) {
				t.Fatalf("Timestamp %s started before ending %s (timestamp=%s)", start.Format("20060102150405"), k, tae.end.Format("20060102150405"))
			}
		}
	}
}

func assertGroup(t *testing.T, expected [][]string, found [][]*tmpFile) {
	if len(expected) != len(found) {
		t.Log(found)
		t.Fatalf("expected %d groups, but found %d", len(expected), len(found))
	}

	for i := range expected {
		elems := make(map[string]int, len(expected[i]))
		for _, e := range expected[i] {
			elems[e]++
		}

		for _, e := range found[i] {
			if _, ok := elems[e.relPath]; !ok {
				t.Fatalf("group %d contains unexpected element %s", i, e.relPath)
			}
			elems[e.relPath]--
			if elems[e.relPath] == 0 {
				delete(elems, e.relPath)
			}
		}
		for e := range elems {
			t.Fatalf("group %d does not contain element %s", i, e)
		}
	}
}

func (s *testRemoteStore) FileMap(opts ...opOption) (map[string]file, error) {
	s.Lock()
	defer s.Unlock()

	if s.failAt == 1 {
		return nil, errors.New("fail")
	}
	c := make(map[string]file)
	for k, v := range s.m {
		c[k] = v
	}
	return c, nil
}

func (s *testRemoteStore) Put(ctx context.Context, f localFile, opts ...opOption) error {
	if s.failAt == 2 {
		return errors.New("fail")
	}
	s.Lock()
	ta := timeActions{
		start: time.Now(),
	}
	s.m[f.Key()] = f
	s.Unlock()
	if s.delayMillis > 0 {
		time.Sleep(s.delayMillis * time.Millisecond)
	}
	s.Lock()
	ta.end = time.Now()
	s.timeActionPut[f.Key()] = ta
	s.Unlock()
	return nil
}

func (s *testRemoteStore) DeleteObjects(ctx context.Context, keys []string, opts ...opOption) error {
	s.Lock()
	defer s.Unlock()

	if s.failAt == 3 {
		return errors.New("fail")
	}
	for _, k := range keys {
		ta := timeActions{
			start: time.Now(),
		}
		delete(s.m, k)
		if s.delayMillis > 0 {
			time.Sleep(s.delayMillis * time.Millisecond)
		}
		ta.end = time.Now()
		s.timeActionDelete[k] = ta
	}
	return nil
}

func (s *testRemoteStore) Finalize() error {
	return nil
}

func newTestDeployer(fc *fileConfig, ls *testLocalStore, store remoteStore) *Deployer {
	d := &Deployer{
		cfg: &Config{
			BucketName:    "example.com",
			RegionName:    "eu-west-1",
			MaxDelete:     300,
			PublicReadACL: true,
			Silent:        true,
			SourcePath:    "/mylocalstore",
			conf:          *fc,
		},
		outv:    ioutil.Discard,
		printer: newPrinter(ioutil.Discard),
		stats:   &DeployStats{},
		local:   ls,
	}
	if store != nil {
		d.store = newStore(*d.cfg, store)
	}
	return d
}

func newTestDefaultFileConfig() *fileConfig {
	return &fileConfig{
		Routes: []*route{
			&route{
				Route: "^.+\\.(js|css|svg|ttf)$",
				Headers: map[string]string{
					"Cache-Control": "max-age=630720000, no-transform, public",
				},
				Gzip: true,
			},
			&route{
				Route: "^.+\\.(png|jpg)$",
				Headers: map[string]string{
					"Cache-Control": "max-age=630720000, no-transform, public",
				},
				Gzip: true,
			},
			&route{
				Route: "^.+\\.(html|xml|json)$",
				Gzip:  true,
			},
		},
	}
}

func newTestFileConfigWithOrder() *fileConfig {
	return &fileConfig{
		Routes: []*route{
			&route{
				Route: "^.+\\.(js|css|svg|ttf)$",
				Headers: map[string]string{
					"Cache-Control": "max-age=630720000, no-transform, public",
				},
				Gzip: true,
			},
			&route{
				Route: "^.+\\.(png|jpg)$",
				Headers: map[string]string{
					"Cache-Control": "max-age=630720000, no-transform, public",
				},
				Gzip: true,
			},
			&route{
				Route: "^.+\\.(html|xml|json)$",
				Gzip:  true,
			},
		},
		Order: []string{
			"^myemptygroup$",
			"^index\\.html$",
		},
	}
}

func newTestDefaultLocalStoreSample() *testLocalStore {
	return newTestLocalStore("/mylocalstore",
		newTestLocalFile(".s3deploy.yml", []byte("my test")),
		newTestLocalFile("index.html", []byte("<html>s3deploy</html>\n")),
		newTestLocalFile("ab.txt", []byte("AB\n")),
		newTestLocalFile("main.css", []byte("ABC")),
	)
}
