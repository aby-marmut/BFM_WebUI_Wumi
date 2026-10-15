package main

// Terminal web interaktif: shell root lewat PTY + WebSocket tulis-tangan (hanya pustaka standar).
//   GET /term                      halaman terminal (perlu sesi login)
//   GET /term/{xterm.js,xterm.css,addon-fit.js}   aset xterm (ditanam di binary, perlu sesi)
//   GET /api/term/ws               WebSocket: frame biner = input/output terminal,
//                                  frame teks = kontrol JSON {"r":[kolom,baris]} atau {"p":1} (jaga koneksi)
// Satu sesi aktif: sambungan baru menggantikan yang lama. Pengaman: sesi login, cek Origin, lanOnly (di main).

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type termSess struct {
	conn   net.Conn
	master *os.File
	cmd    *exec.Cmd
	wmu    sync.Mutex
	once   sync.Once
}

var (
	termMu  sync.Mutex
	termCur *termSess
	errWS   = errors.New("websocket: frame tidak valid")
)

type termWinsz struct {
	Row, Col, X, Y uint16
}

func (s *termSess) close() {
	s.once.Do(func() {
		if s.conn != nil {
			s.conn.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			s.cmd.Process.Kill()
		}
		if s.master != nil {
			s.master.Close()
		}
	})
}

// send menulis satu frame WebSocket (server -> klien, tanpa mask).
func (s *termSess) send(op byte, p []byte) error {
	n := len(p)
	h := make([]byte, 0, 10+n)
	h = append(h, 0x80|op)
	switch {
	case n < 126:
		h = append(h, byte(n))
	case n < 65536:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		h = append(h, 127)
		h = append(h, b[:]...)
	}
	h = append(h, p...)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := s.conn.Write(h)
	return err
}

// wsReadFrame membaca satu frame klien (wajib bermask, tanpa fragmentasi, maks 1 MB).
func wsReadFrame(rd *bufio.Reader) (byte, []byte, error) {
	var hd [2]byte
	if _, err := io.ReadFull(rd, hd[:]); err != nil {
		return 0, nil, err
	}
	if hd[0]&0x80 == 0 || hd[0]&0x70 != 0 || hd[1]&0x80 == 0 {
		return 0, nil, errWS
	}
	op := hd[0] & 0x0f
	n := uint64(hd[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(rd, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(rd, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<20 {
		return 0, nil, errWS
	}
	var mk [4]byte
	if _, err := io.ReadFull(rd, mk[:]); err != nil {
		return 0, nil, err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(rd, data); err != nil {
		return 0, nil, err
	}
	for i := range data {
		data[i] ^= mk[i&3]
	}
	return op, data, nil
}

func ptyIoctl(fd, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// termOpenPTY membuka pasangan master/slave lewat /dev/ptmx (TIOCSPTLCK=0x40045431, TIOCGPTN=0x80045430).
func termOpenPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ptyIoctl(m.Fd(), 0x40045431, unsafe.Pointer(&unlock)); err != nil {
		m.Close()
		return nil, nil, err
	}
	var n uint32
	if err := ptyIoctl(m.Fd(), 0x80045430, unsafe.Pointer(&n)); err != nil {
		m.Close()
		return nil, nil, err
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

// termResize mengatur ukuran jendela PTY (TIOCSWINSZ=0x5414).
func termResize(m *os.File, cols, rows int) {
	if cols < 1 || cols > 500 || rows < 1 || rows > 500 {
		return
	}
	ws := termWinsz{Row: uint16(rows), Col: uint16(cols)}
	ptyIoctl(m.Fd(), 0x5414, unsafe.Pointer(&ws))
}

func termSameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func termWS(w http.ResponseWriter, r *http.Request) {
	if !validSession(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if !termSameOrigin(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "bad request", 400)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack tidak didukung", 500)
		return
	}
	m, sl, err := termOpenPTY()
	if err != nil {
		http.Error(w, "pty: "+err.Error(), 500)
		return
	}
	termResize(m, 80, 24)
	cmd := exec.Command("/system/bin/sh")
	cmd.Dir = "/data/local/tmp"
	path := "/system/bin:/system/xbin:/vendor/bin:/data/adb/magisk:/data/adb/ksu/bin:/data/adb/ap/bin"
	if p := os.Getenv("PATH"); p != "" {
		path += ":" + p
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "HOME=/data/local/tmp", "TMPDIR=/data/local/tmp", "SHELL=/system/bin/sh", "PATH="+path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = sl, sl, sl
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		sl.Close()
		m.Close()
		http.Error(w, "shell: "+err.Error(), 500)
		return
	}
	sl.Close()
	go cmd.Wait()
	s := &termSess{master: m, cmd: cmd}
	conn, brw, err := hj.Hijack()
	if err != nil {
		s.close()
		return
	}
	s.conn = conn
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	conn.SetDeadline(time.Time{})
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
	if err != nil {
		s.close()
		return
	}
	termMu.Lock()
	old := termCur
	termCur = s
	termMu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		s.close()
		termMu.Lock()
		if termCur == s {
			termCur = nil
		}
		termMu.Unlock()
	}()
	log.Println("terminal dibuka dari", r.RemoteAddr)

	// shell -> klien
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := m.Read(buf)
			if n > 0 {
				if s.send(2, buf[:n]) != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		s.send(8, []byte{0x03, 0xe8})
		s.close()
	}()

	// klien -> shell
	rd := brw.Reader
loop:
	for {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		op, data, err := wsReadFrame(rd)
		if err != nil {
			break
		}
		switch op {
		case 2:
			if _, err := m.Write(data); err != nil {
				break loop
			}
		case 1:
			var c struct {
				R []int `json:"r"`
			}
			if json.Unmarshal(data, &c) == nil && len(c.R) == 2 {
				termResize(m, c.R[0], c.R[1])
			}
		case 8:
			break loop
		case 9:
			s.send(10, data)
		}
	}
}

func termPage(w http.ResponseWriter, r *http.Request) {
	if !validSession(r) {
		http.Redirect(w, r, "/login", 302)
		return
	}
	b, err := webFS.ReadFile("web/term.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func termAsset(w http.ResponseWriter, r *http.Request) {
	if !validSession(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/term/")
	ct := ""
	switch name {
	case "xterm.js", "addon-fit.js":
		ct = "application/javascript"
	case "xterm.css":
		ct = "text/css"
	default:
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/term/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(b)
}

func registerTerm(mux *http.ServeMux) {
	mux.HandleFunc("/term", termPage)
	mux.HandleFunc("/term/", termAsset)
	mux.HandleFunc("/api/term/ws", termWS)
}
