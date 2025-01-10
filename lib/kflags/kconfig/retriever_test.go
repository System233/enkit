package kconfig

import (
	"github.com/System233/enkit/lib/cache"
	"github.com/System233/enkit/lib/khttp/downloader"
	"github.com/System233/enkit/lib/khttp/ktest"
	"github.com/System233/enkit/lib/logger"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/url"
	"sync"
	"testing"
	"time"
)

var message = "Be realistic, demand the impossible!"

func testEncoding(t *testing.T, retriever func(message, encoding string) Retriever, waiter func()) {
	var v string
	var e error
	callback := func(_, value string, err error) {
		v = value
		e = err
	}

	retriever(message, "file").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	file1 := v

	retriever(message, "file").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	file2 := v

	// Note that if the same data is passed as a flag, the same file should be re-used.
	assert.Equal(t, file1, file2)

	data, err := ioutil.ReadFile(file1)
	assert.Nil(t, err)
	assert.Equal(t, message, string(data))

	retriever(message, "").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	assert.Equal(t, message, v)

	retriever(message, "string").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	assert.Equal(t, message, v)

	retriever(message, "invalid").Retrieve(callback)
	waiter()
	assert.NotNil(t, e)

	retriever(message, "hex").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	assert.Equal(t, "4265207265616c69737469632c2064656d616e642074686520696d706f737369626c6521", v)

	retriever(message, "base64").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	assert.Equal(t, "QmUgcmVhbGlzdGljLCBkZW1hbmQgdGhlIGltcG9zc2libGUh", v)

	retriever(message, "base64url").Retrieve(callback)
	waiter()
	assert.Nil(t, e)
	assert.Equal(t, "QmUgcmVhbGlzdGljLCBkZW1hbmQgdGhlIGltcG9zc2libGUh", v)

}

func TestInlineRetriever(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}

	testEncoding(t, func(message, encoding string) Retriever {
		return NewInlineRetriever(c, &Parameter{
			Name:     "name",
			Value:    message,
			Encoding: EncodeAs(encoding),
		})
	}, func() {})
}

func TestURLRetriever(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}
	dl, err := downloader.New()
	assert.Nil(t, err)

	http := ktest.Capture(ktest.CachableStringHandler(message))
	_, url, err := ktest.StartServerURL(http.Handle)
	assert.Nil(t, err)

	type result struct {
		value string
		err   error
	}

	l := sync.Mutex{}
	values := []result{}
	callback := func(_, value string, err error) {
		l.Lock()
		defer l.Unlock()
		values = append(values, result{value: value, err: err})
	}

	r := NewURLRetriever(logger.Nil, c, dl, url, &Parameter{
		Name:  "name",
		Value: url.String(),
	})
	for ix := 0; ix < 10; ix++ {
		r.Retrieve(callback)
	}
	dl.Wait()

	// Result has been provided 10 times via callbacks, but only fetched once via http.
	assert.Equal(t, 10, len(values))
	assert.Equal(t, 1, len(http.Request))

	for ix := 0; ix < 10; ix++ {
		t.Run("result %d", func(t *testing.T) {
			assert.Equal(t, nil, values[ix].err)
			assert.Equal(t, message, values[ix].value)
		})
	}

	testEncoding(t, func(message, encoding string) Retriever {
		return NewURLRetriever(logger.Nil, c, dl, url, &Parameter{
			Name:     "name",
			Value:    url.String(),
			Encoding: EncodeAs(encoding),
		})
	}, func() {
		dl.Wait()
	})
}

func TestURLByHash(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}
	dl, err := downloader.New()
	assert.Nil(t, err)
	http := ktest.Capture(ktest.StringHandler(message))
	_, url, err := ktest.StartServerURL(http.Handle)
	assert.Nil(t, err)

	type result struct {
		value string
		err   error
	}

	l := sync.Mutex{}
	values := []result{}
	callback := func(_, value string, err error) {
		l.Lock()
		defer l.Unlock()
		values = append(values, result{value: value, err: err})
	}

	r := NewURLRetriever(logger.Nil, c, dl, url, &Parameter{
		Name:  "name",
		Value: url.String(),
		Hash:  "c24e00ca3ba81c6b4071298fadcefbec2b560f13d40dff7c1881989add11c75f",
	})
	for ix := 0; ix < 10; ix++ {
		r.Retrieve(callback)
	}
	dl.Wait()

	// Result has been provided 10 times via callbacks, but only fetched once via http.
	assert.Equal(t, 10, len(values))
	for ix, ret := range values {
		assert.Equal(t, nil, ret.err, "element %d - %s - %#v", ix, ret.err, ret.value)
	}
	assert.Equal(t, 1, len(http.Request))
}

func TestCreator(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}
	dl, err := downloader.New()
	assert.Nil(t, err)

	creator := NewCreator(logger.Nil, c, dl)
	r, err := creator.Create(nil, &Parameter{})
	assert.NotNil(t, err)
	assert.Nil(t, r)

	r, err = creator.Create(nil, &Parameter{Source: "proletarian"})
	assert.NotNil(t, err)
	assert.Nil(t, r)

	r, err = creator.Create(nil, &Parameter{Source: SourceURL, Name: "union"})
	assert.NotNil(t, err)
	assert.Nil(t, r)

	testEncoding(t, func(message, encoding string) Retriever {
		r, err := creator.Create(nil, &Parameter{
			Name:     "name",
			Value:    message,
			Encoding: EncodeAs(encoding),
		})
		assert.Nil(t, err)
		return r
	}, func() {
	})

	http := ktest.Capture(ktest.CachableStringHandler(message))
	_, url, err := ktest.StartServerURL(http.Handle)
	assert.Nil(t, err)

	testEncoding(t, func(message, encoding string) Retriever {
		r, err := creator.Create(nil, &Parameter{
			Name:     "name",
			Value:    url.String(),
			Source:   SourceURL,
			Encoding: EncodeAs(encoding),
		})
		assert.Nil(t, err)
		return r
	}, func() {
		dl.Wait()
	})

	r1, err := creator.Create(nil, &Parameter{Source: SourceURL, Name: "union", Value: url.String()})
	assert.Nil(t, err)
	assert.NotNil(t, r1)

	r2, err := creator.Create(nil, &Parameter{Source: SourceURL, Name: "bar", Value: url.String()})
	assert.Nil(t, err)
	assert.NotNil(t, r2)

	// No matter the name, if the same url with the same encoding is fetched, the same retrievers should be used.
	assert.Equal(t, r1, r2)

	r3, err := creator.Create(nil, &Parameter{Source: SourceURL, Name: "bar", Value: url.String(), Encoding: "file"})
	assert.Nil(t, err)
	assert.NotNil(t, r3)

	assert.NotEqual(t, r1, r3)
}

func TestURLFail(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}
	dl, err := downloader.New()
	assert.Nil(t, err)

	type result struct {
		value string
		err   error
	}

	l := sync.Mutex{}
	values := []result{}
	callback := func(_, value string, err error) {
		l.Lock()
		defer l.Unlock()
		values = append(values, result{value: value, err: err})
	}
	u, err := url.Parse("https://127.0.0.3:1/")
	assert.Nil(t, err)

	r := NewURLRetriever(logger.Nil, c, dl, u, &Parameter{
		Name:  "name",
		Value: u.String(),
	})
	r.Retrieve(callback)
	dl.Wait()

	assert.Equal(t, 1, len(values))
	assert.NotNil(t, values[0].err)
}

// Verify that pipelining of requests is handled correctly.
func TestURLSlow(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "cache")
	assert.Nil(t, err)
	c := &cache.Local{Root: tempdir}
	dl, err := downloader.New()
	assert.Nil(t, err)

	http := ktest.Capture(ktest.CachableStringHandler(message))
	_, url, err := ktest.StartServerURL(ktest.Slow(100*time.Millisecond, http.Handle))
	assert.Nil(t, err)

	type result struct {
		value string
		err   error
	}

	l := sync.Mutex{}
	values := []result{}
	callback := func(_, value string, err error) {
		l.Lock()
		defer l.Unlock()
		values = append(values, result{value: value, err: err})
	}

	for ix := 0; ix < 10; ix++ {
		NewURLRetriever(logger.Nil, c, dl, url, &Parameter{
			Name:  "name",
			Value: url.String(),
		}).Retrieve(callback)
	}
	dl.Wait()

	assert.Equal(t, 10, len(values))
	for ix := 0; ix < len(values); ix++ {
		assert.Nil(t, values[ix].err)
		assert.Equal(t, message, values[ix].value)
	}
}
