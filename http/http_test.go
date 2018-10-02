package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ipfs/go-ipfs-cmds"
	"github.com/ipfs/go-ipfs-files"
)

func TestHTTP(t *testing.T) {
	type testcase struct {
		path         []string
		v            interface{}
		vs           []interface{}
		file         files.File
		r            string
		err          error
		sendErr      error
		wait         bool
		expectHeader map[string]string
	}

	tcs := []testcase{
		{
			path: []string{"version"},
			v: &VersionOutput{
				Version: "0.1.2",
				Commit:  "c0mm17",
				Repo:    "4",
				System:  runtime.GOARCH + "/" + runtime.GOOS, //TODO: Precise version here
				Golang:  runtime.Version(),
			},
		},

		{
			path:    []string{"error"},
			sendErr: errors.New("an error occurred"),
		},

		{
			path: []string{"doubleclose"},
			v:    "some value",
		},

		{
			path: []string{"single"},
			v:    "some value",
			wait: true,
		},

		{
			path: []string{"reader"},
			r:    "the reader call returns a reader.",
		},

		{
			path: []string{"echo"},
			file: files.NewSliceFile(
				"",
				"",
				[]files.File{
					files.NewReaderFile(
						"stdin",
						"/dev/stdin",
						readCloser{
							Reader: bytes.NewBufferString("This is the body of the request!"),
							Closer: nopCloser{},
						}, nil)}),
			vs: []interface{}{"i received:", "This is the body of the request!"},
			expectHeader: map[string]string{
				"Connection": "close",
			},
		},
	}

	mkTest := func(tc testcase) func(*testing.T) {
		return func(t *testing.T) {
			env, srv := getTestServer(t, nil) // handler_test:/^func getTestServer/
			c := NewClient(srv.URL)
			req, err := cmds.NewRequest(context.Background(), tc.path, nil, nil, nil, cmdRoot)
			if err != nil {
				t.Fatal(err)
			}

			if tc.file != nil {
				req.Files = tc.file
			}

			res, err := c.Send(req)
			if tc.sendErr != nil {
				if err == nil {
					t.Fatalf("expected error %q but got nil", tc.sendErr)
				}

				if err.Error() != tc.sendErr.Error() {
					t.Fatalf("expected error %q but got %q", tc.sendErr, err)
				}

				return
			} else if err != nil {
				t.Fatal("unexpected error:", err)
			}

			if len(tc.vs) > 0 {
				for _, tc.v = range tc.vs {
					v, err := res.Next()
					if err != nil {
						t.Error("unexpected error:", err)
					}
					// TODO find a better way to solve this!
					if s, ok := v.(*string); ok {
						v = *s
					}
					t.Log("v:", v, "err:", err)

					// if we don't expect a reader
					if !reflect.DeepEqual(v, tc.v) {
						t.Errorf("expected value to be %v but got: %+v", tc.v, v)
					}
				}

				_, err = res.Next()
				if tc.err != nil {
					if err == nil {
						t.Fatal("got nil error, expected:", tc.err)
					} else if err.Error() != tc.err.Error() {
						t.Fatalf("got error %q, expected %q", err, tc.err)
					}
				} else if err != io.EOF {
					t.Fatal("expected io.EOF error, got:", err)
				}
			} else if len(tc.r) == 0 {
				v, err := res.Next()
				// TODO find a better way to solve this!
				if s, ok := v.(*string); ok {
					v = *s
				}

				t.Log("v:", v, "err:", err)
				if tc.err != nil {
					if err == nil {
						t.Error("got nil error, expected:", tc.err)
					} else if err.Error() != tc.err.Error() {
						t.Errorf("got error %q, expected %q", err, tc.err)
					}
				} else if err != nil {
					t.Fatal("unexpected error:", err)
				}

				// if we don't expect a reader
				if !reflect.DeepEqual(v, tc.v) {
					t.Errorf("expected value to be %v but got: %+v", tc.v, v)
				}

				_, err = res.Next()
				if tc.err != nil {
					if err == nil {
						t.Fatal("got nil error, expected:", tc.err)
					} else if err.Error() != tc.err.Error() {
						t.Fatalf("got error %q, expected %q", err, tc.err)
					}
				} else if err != io.EOF {
					t.Fatal("expected io.EOF error, got:", err)
				}
			} else {
				v, err := res.Next()
				// TODO find a better way to solve this!
				if s, ok := v.(*string); ok {
					v = *s
				}

				t.Log("v:", v, "err:", err)
				if tc.err != nil {
					if err == nil {
						t.Error("got nil error, expected:", tc.err)
					} else if err.Error() != tc.err.Error() {
						t.Errorf("got error %q, expected %q", err, tc.err)
					}
				} else if err != nil {
					t.Fatal("unexpected error:", err)
				}

				r, ok := v.(io.Reader)
				if !ok {
					t.Fatalf("expected a %T but got a %T", r, v)
				}

				var buf bytes.Buffer

				_, err = io.Copy(&buf, r)
				if err != nil {
					t.Fatal("unexpected copy error:", err)
				}

				if buf.String() != tc.r {
					t.Errorf("expected return string %q but got %q", tc.r, buf.String())
				}
			}

			httpRes := res.(*Response)
			for h, hexp := range tc.expectHeader {
				hdr := httpRes.res.Header.Get(h)
				if hdr != hexp {
					t.Errorf("expected header %q to be %q but got %q", h, hexp, hdr)
				}
			}
			wait, ok := getWaitChan(env)
			if !ok {
				t.Fatal("could not get wait chan")
			}

			if tc.wait {
				<-wait
			}
		}
	}

	for i, tc := range tcs {
		t.Run(fmt.Sprintf("%d-%s", i, strings.Join(tc.path, "/")), mkTest(tc))
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

type readCloser struct {
	io.Reader
	io.Closer
}
