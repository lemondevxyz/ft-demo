package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/sse"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hanwen/go-fuse/v2/fuse/nodefs"
	"github.com/hanwen/go-fuse/v2/fuse/pathfs"
	"github.com/lemondevxyz/aferofuse"
	capacityfs "github.com/lemondevxyz/capacity-fs"
	"github.com/spf13/afero"
	"github.com/thanhpk/randstr"
)

type data *map[string]interface{}
type dataMap map[string]data

func getNetworkAddr(name string) (string, string, error) {
	val := os.Getenv(name)
	if len(val) == 0 || len(name) == 0 {
		return "", "", io.EOF
	}

	split := strings.Split(val, "!")
	if len(split) < 2 {
		return "tcp", val, nil
	}

	return split[0], split[1], nil
}

func getNetwork() (net.Listener, *http.Transport) {
	cliNet, cliAddr, err := getNetworkAddr("FT_ADDR")
	if err != nil {
		log.Fatalln("Please set the FT_ADDR variable")
	}

	srvNet, srvAddr, err := getNetworkAddr("FT_DEMO_ADDR")
	if err != nil {
		log.Fatalln("Please set the FT_DEMO_ADDR variable")
	}

	ln, err := net.Listen(srvNet, srvAddr)
	if err != nil {
		log.Fatalln(err)
	}

	dialer := &net.Dialer{}
	return ln, &http.Transport{
		Dial: func(network, addr string) (net.Conn, error) { return dialer.Dial(cliNet, cliAddr) },
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, cliNet, cliAddr)
		},
	}
}

type templateFs struct {
	afero.Fs
	template afero.Fs
}

func (t templateFs) Mkdir(name string, perm os.FileMode) error {
	err := t.Fs.Mkdir(name, perm)
	if err != nil {
		_, staterr := t.Fs.Stat(name)
		if staterr != nil {
			return err
		}
	}

	slice := strings.Split(name, "/")
	if len(slice) <= 2 {
		files, _ := afero.ReadDir(t, slice[len(slice)-1])
		if len(files) > 0 {
			return nil
		}

		containerFs := afero.NewBasePathFs(t, name)
		return afero.Walk(t.template, "", func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if (info.Mode() & fs.ModeSymlink) != 0 {
				linkreader := t.template.(afero.LinkReader)
				link, _ := linkreader.ReadlinkIfPossible(path)
				linker := afero.NewBasePathFs(t.Fs, name).(afero.Linker)

				return linker.SymlinkIfPossible(link, path)
			} else if info.Mode().IsDir() {
				_, err := t.Stat(path)
				if err != nil {
					return afero.NewBasePathFs(t.Fs, name).Mkdir(path, perm)
				}

				return err
			}

			rd, err := t.template.OpenFile(path, os.O_RDONLY, info.Mode())
			if err != nil {
				return err
			}
			defer rd.Close()

			wr, err := containerFs.OpenFile(path, os.O_WRONLY|os.O_CREATE, info.Mode())
			if err != nil {
				return err
			}
			defer wr.Close()

			_, err = io.Copy(wr, rd)

			return err
		})
	}

	return nil
}

func getFuse(credsMap *sync.Map) (afero.Fs, *fuse.Server) {
	amount := len(os.Args)
	if amount < 4 {
		log.Fatalln("Please provide at-least 3 arguments: [container_dir] [fuse_dir] [template_dir]")
	}

	container, fuse, template := os.Args[1], os.Args[2], os.Args[3]
	containerFs := templateFs{
		Fs: afero.NewBasePathFs(afero.NewOsFs(), container),
		template: afero.NewBasePathFs(afero.NewOsFs(), template),
	}

	os.MkdirAll(container, 0755)
	os.MkdirAll(fuse, 0755)

	files, _ := afero.ReadDir(containerFs, ".")
	for _, v := range files {
		containerFs.RemoveAll(v.Name())
	}

	mynodefs := pathfs.NewPathNodeFs(aferofuse.NewFuseFileSystem(matcherFs{
		containerFs,
		credsMap,
	}), &pathfs.PathNodeFsOptions{
		ClientInodes: true,
	})
	exec.Command("fusermount", "-u", fuse).Run()
	server, _, err := nodefs.MountRoot(fuse, mynodefs.Root(), &nodefs.Options{})
	if err != nil {
		log.Fatalln("Mount fail", err)
	}

	return containerFs, server
}

func chanAdd(m *sync.Map) chan *sse.Event {
	ch := make(chan *sse.Event)
	m.Store(ch, nil)
	return ch
}
func chanRm(m *sync.Map, ch chan *sse.Event) { m.Delete(ch) }

type stringModifier func(string) string

func (s stringModifier) sliceModifier(v []string) []string {
	if v == nil {
		return []string{}
	}

	for _, val := range v {
		s(val)
	}

	return v
}

func setIfExists(m data, key string, val interface{}) {
	_, ok := (*m)[key]
	if !ok {
		return
	}

	(*m)[key] = val
}

func getOrEmptySlice(m data, key string) []string {
	val, ok := (*m)[key]
	if !ok {
		return nil
	}

	res, ok := val.([]string)
	return res
}

func getOrEmptyString(m data, key string) string {
	val, ok := (*m)[key]
	if !ok {
		return ""
	}

	str, ok := val.(string)
	return str
}

func modifyData(m data, fn stringModifier) {
	for k := range *m {
		if k != strings.ToLower(k) {
			(*m)[strings.ToLower(k)] = (*m)[k]
			delete((*m), k)
		}
	}

	srcStr, srcSlice := getOrEmptyString(m, "src"), getOrEmptySlice(m, "src")
	if srcStr != "" {
		setIfExists(m, "src", srcStr)
	} else if srcSlice != nil {
		setIfExists(m, "src", srcSlice)
	}

	setIfExists(m, "name", fn(getOrEmptyString(m, "name")))
	setIfExists(m, "srcs", fn.sliceModifier(getOrEmptySlice(m, "srcs")))
	setIfExists(m, "dst", fn(getOrEmptyString(m, "dst")))
}

type credentials struct {
	id  string
	ops *sync.Map
	chs *sync.Map
	afs afero.Fs
}

func generateId() string { return randstr.String(5) }

func authMiddleware(m *sync.Map, w http.ResponseWriter, r *http.Request) *credentials {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)

	val, ok := m.Load(ip)
	if !ok {
		creds := &credentials{id: generateId(), ops: &sync.Map{}, chs: &sync.Map{}}
		m.Store(ip, creds)
		return creds
	}

	return val.(*credentials)
}

type belongsTo func(id string) bool

func filterData(fn belongsTo, event string, val interface{}) *sse.Event {
	str := val.(string)
	switch {
	case event == "operation-all":
		ret := dataMap{}

		err := json.Unmarshal([]byte(str), ret)
		if err != nil {
			return nil
		}

		for k, v := range ret {
			if fn(getOrEmptyString(v, "id")) {
				ret[k] = v
			}
		}
		// a
		return &sse.Event{Event: event, Data: ret}
	case strings.HasPrefix(event, "operation"):
		if event != "operation-done" {
			ret := data(&map[string]interface{}{})

			err := json.Unmarshal([]byte(str), ret)
			if err != nil {
				return nil
			}

			if fn(getOrEmptyString(ret, "id")) {
				return &sse.Event{Event: event, Data: ret}
			} else {
				return nil
			}
		}

		fallthrough
	case strings.HasPrefix(event, "fs"):
		if event == "fs-move" {
			ret := data(&map[string]interface{}{})

			err := json.Unmarshal([]byte(str), ret)
			if err != nil {
				return nil
			}

			if fn(getOrEmptyString(ret, "old")) && fn(getOrEmptyString(ret, "new")) {
				return &sse.Event{
					Event: event,
					Data:  val,
				}
			}
		}

		id, _ := val.(string)
		if fn(id) {
			return &sse.Event{Event: event, Data: id}
		}
	}

	return nil
}

func rangeOverChs(m *sync.Map, fn func(*credentials, chan *sse.Event)) {
	m.Range(func(_, v interface{}) bool {
		creds := v.(*credentials)

		creds.chs.Range(func(k, _ interface{}) bool {
			ch, _ := k.(chan *sse.Event)
			fn(creds, ch)

			return true
		})

		return true
	})

}

func listenForSSE(m *sync.Map, resp *http.Response, setOps func(val dataMap), setId func(string)) {
	// a
	p := make([]byte, 32768)
loop:
	for {
		n, err := resp.Body.Read(p)
		if err != nil {
			rangeOverChs(m, func(creds *credentials, ch chan *sse.Event) {
				if ch != nil {
					close(ch)
				}
				creds.ops.Delete(ch)
			})
			return
		}

		evs, err := sse.Decode(bytes.NewBuffer(p[:n]))
		if err == nil && len(evs) > 0 {
			for _, v := range evs {
				if v.Event == "operation-all" {
					val := dataMap{}
					err := json.Unmarshal([]byte(v.Data.(string)), &val)
					if err != nil {
						panic(err)
					}

					setOps(val)
				}

				if v.Event == "id" {
					setId(v.Data.(string))
					continue loop
				}
			}

			time.Sleep(time.Millisecond * 50)
			rangeOverChs(m, func(creds *credentials, ch chan *sse.Event) {
				for _, v := range evs {
					ev := filterData(func(id string) bool {
						_, ok := creds.ops.Load(id)
						return ok
					}, v.Event, v.Data)

					time.Sleep(time.Millisecond * 50)
					if ev != nil {
						ch <- ev
					}
				}
			})
		}
	}
}

func sseRequest(creds *credentials, ops dataMap, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	flusher := w.(http.Flusher)
	flusher.Flush()
	ch := make(chan *sse.Event)

	go func() {
		<-time.After(time.Millisecond * 50)
		ch <- &sse.Event{
			Event: "id",
			Data:  creds.id,
		}
		<-time.After(time.Millisecond * 50)
		ch <- &sse.Event{
			Event: "operation-all",
			Data:  ops,
		}
	}()

	defer creds.chs.Delete(ch)
	creds.chs.Store(ch, nil)

	closed := r.Context().Done()
	for {
		select {
		case <-closed:
			return
		case ev, ok := <-ch:
			if ok {
				sse.Encode(w, *ev)
			}
			flusher.Flush()
		}
	}
}

type matcherFs struct {
	afero.Fs
	credsMap *sync.Map
}

func (m matcherFs) getId(name string) string {
	name = path.Clean(name)
	if name[0] == '/' || name[0] == '.' {
		name = name[1:]
	}
	split := strings.Split(name, "/")
	if len(split) > 0 {
		return split[0]
	}

	return name
}

func (m matcherFs) getFsById(name string) (afs afero.Fs) {
	afs = m.Fs
	m.credsMap.Range(func(k interface{}, v interface{}) bool {
		creds := v.(*credentials)
		if name == creds.id {
			afs = creds.afs
			return false
		}

		return true
	})

	return
}

func (m matcherFs) modifyNameIfNecessary(afs afero.Fs, name string) string {
	if afs == m.Fs {
		return name
	}

	split := strings.Split(name, "/")
	return strings.Join(split[1:], "/")
}

func (m matcherFs) Create(name string) (afero.File, error) {
	afs := m.getFsById(m.getId(name))

	return afs.Create(m.modifyNameIfNecessary(afs, name))
}
func (m matcherFs) Mkdir(name string, perm os.FileMode) error {
	afs := m.getFsById(m.getId(name))

	return afs.Mkdir(m.modifyNameIfNecessary(afs, name), perm)
}
func (m matcherFs) MkdirAll(name string, perm os.FileMode) error {
	afs := m.getFsById(m.getId(name))

	return afs.MkdirAll(m.modifyNameIfNecessary(afs, name), perm)
}

func (m matcherFs) Open(name string) (afero.File, error) {
	afs := m.getFsById(m.getId(name))

	return afs.Open(m.modifyNameIfNecessary(afs, name))
}
func (m matcherFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	afs := m.getFsById(m.getId(name))

	return afs.OpenFile(m.modifyNameIfNecessary(afs, name), flag, perm)
}
func (m matcherFs) Remove(name string) error {
	afs := m.getFsById(m.getId(name))

	return afs.Remove(m.modifyNameIfNecessary(afs, name))
}
func (m matcherFs) RemoveAll(name string) error {
	afs := m.getFsById(m.getId(name))

	return afs.RemoveAll(m.modifyNameIfNecessary(afs, name))
}

func main() {
	ln, transport := getNetwork()
	defer ln.Close()
	client := &http.Client{Transport: transport}

	credsMap := &sync.Map{}
	fs, serv := getFuse(credsMap)
	go serv.Serve()
	serv.WaitMount()
	defer serv.Unmount()

	server := http.Server{}

	_ops := dataMap{}
	opsMtx := sync.Mutex{}
	getOps := func() dataMap {
		opsMtx.Lock()
		defer opsMtx.Unlock()
		return _ops
	}
	setOps := func(val dataMap) {
		opsMtx.Lock()
		_ops = val
		opsMtx.Unlock()
	}
	_id := ""
	idMtx := sync.Mutex{}
	setId := func(val string) {
		idMtx.Lock()
		_id = val
		idMtx.Unlock()
	}
	getId := func() string {
		idMtx.Lock()
		defer idMtx.Unlock()
		return _id
	}

	resp, err := client.Get("http://host/api/v0/sse")
	if err != nil {
		log.Fatalln(err)
	}

	go listenForSSE(credsMap, resp, setOps, setId)

	const maxSize = 1024 * 512 // 512KB
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:8080")
		creds := authMiddleware(credsMap, w, r)
		fs.Mkdir(creds.id, 0755)
		if creds.afs == nil {
			afs, err := capacityfs.NewCapacityFs(afero.NewBasePathFs(fs, creds.id), maxSize)
			if err != nil {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(capacityfs.ErrNotEnoughCapacity.Error()))
				return
			}
			creds.afs = afs
		}

		if r.RequestURI == "/api/v0/sse" {
			sseRequest(creds, getOps(), w, r)

			return
		}

		buf := bytes.NewBuffer(nil)
		if strings.HasPrefix(r.RequestURI, "/api/v0") {
			input := map[string]interface{}{}

			dec := json.NewDecoder(r.Body)
			defer r.Body.Close()
			if err := dec.Decode(&input); err != nil {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(400)
				w.Write([]byte("only json data is accepted"))

				return
			}

			modifyData(&input, func(val string) string {
				val = path.Clean(val)
				if len(val) > 0 &&
					(val[0] == '/' || val[0] == '.') {
					val = val[1:]
				}

				splt := strings.Split(val, "/")
				if (len(splt) >= 1 && splt[0] != creds.id) || len(splt) == 0 {
					splt = append([]string{creds.id}, splt...)
				}

				return strings.Join(splt, "/")
			})
			setIfExists(&input, "writer_id", getId())

			if strings.HasPrefix(r.RequestURI, "/api/v0/op") &&
				r.RequestURI != "/api/v0/op/new" {
				_, ok := creds.ops.Load(getOrEmptyString(&input, "id"))
				if !ok {
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte("that operation does not belong to you..."))
					return
				}
			}

			if err := json.NewEncoder(buf).Encode(input); err != nil {
				panic(err)
			}
		}

		if strings.HasPrefix(r.RequestURI, "/files") {
			r.RequestURI = path.Clean(r.RequestURI)
			split := strings.Split(r.RequestURI, "/")
			if len(split) <= 2 {
				split = append(split, creds.id)
			} else if len(split) > 2 && split[2] != creds.id {
				split = append([]string{"", "files", creds.id}, split[2:]...)
			}
			r.RequestURI = strings.Join(split, "/")
		}

		req, err := http.NewRequest(r.Method, "http://host"+r.RequestURI, buf)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(500)
			w.Write([]byte(fmt.Sprintf("internal NewRequest error: %s", err.Error())))
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(500)
			w.Write([]byte(fmt.Sprintf("internal request error: %s", err.Error())))
			return
		}

		buf.Reset()
		io.Copy(buf, resp.Body)
		resp.Body.Close()

		if r.RequestURI == "/api/v0/op/new" && resp.StatusCode == 200 {
			val := data(&map[string]interface{}{})

			if err := json.Unmarshal(buf.Bytes(), val); err != nil {
				panic(err)
			}

			id := getOrEmptyString(val, "id")
			creds.ops.Store(id, nil)
		}

		for k := range resp.Header {
			w.Header().Set(k, resp.Header.Get(k))
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, buf)
		resp.Body.Close()
	})
	defer server.Close()
	server.Serve(ln)
}
