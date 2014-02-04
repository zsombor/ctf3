package transport

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"os"
	"time"
	"path/filepath"
	"fmt"
)

var unix *regexp.Regexp = regexp.MustCompile("^[/a-zA-Z0-9\\.]*$")

func UnixDialer(_, encoded string) (net.Conn, error) {
	decoded := Decode(encoded)
	return net.Dial(Network(decoded), decoded)
}

func TimeoutDialer(conTimeout, rwTimeout time.Duration) func(_, addr string) (c net.Conn, err error) {
	return func(_, encoded string) (net.Conn, error) {
		decoded := Decode(encoded)
		conn, err:= net.DialTimeout(Network(decoded), decoded, conTimeout)
		if err != nil {
			return nil, err
		}
		conn.SetDeadline(time.Now().Add(rwTimeout))
		return conn, nil
	}
}

func ConnectionString(listen string) string {
	relListen := listen
	if "zsombor" != os.Getenv("USER"){
		// // Listen is relative to the current directory.
		cwd, _ := os.Getwd()
		dir := filepath.Base(cwd)
		relListen = fmt.Sprintf("./../%s/%s.sock", dir, dir)	
	}
	cs, _ := Encode(relListen)
	return cs
}


func Network(addr string) string {
	if addr[0] == '/' || addr[0] == '.' {
		return "unix"
	} else {
		return "tcp"
	}
}

func Encode(addr string) (string, error) {
	switch Network(addr) {
	case "unix":
		if !unix.MatchString(addr) {
			return "", errors.New("Invalid address path " + addr + " (must contain only dots, slashes, and alphanumeric characters, due to the way we're hacking HTTP-over-Unix-sockets into Go)")
		}
		addr = strings.Replace(addr, "/", "-", -1)
	case "tcp":
		if addr[0] == '-' {
			return "", errors.New("Invalid address " + addr + " (cannot begin with a -, due to the way we're hacking HTTP-over-Unix-sockets into Go)")
		}
	}

	return "http://" + addr, nil
}

func Decode(addr string) string {
	// Nuke the http:// if needed (may be removed by the HTTP
	// library)
	addr = strings.TrimPrefix(addr, "http://")

	if addr[0] == '-' || addr[0] == '.' {
		// Unix address
		// Remove a port, if it's been added by the HTTP library
		addr = strings.SplitN(addr, ":", 2)[0]

		// Actually decode
		addr = strings.Replace(addr, "-", "/", -1)
	}

	return addr
}
