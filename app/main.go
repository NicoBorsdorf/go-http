package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
)

type Server struct {
	Addr    string
	Handler http.Handler
}

var dir = flag.String("directory", "", "")

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Println("Logs from your program will appear here!")

	// Uncomment this block to pass the first stage
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to bind to port 4221")
		os.Exit(1)
	}
	fmt.Println("Listening on Port 4221")

	flag.Parse()

	// making files dirctory
	if string(*dir) != "" {
		err = os.Mkdir(string(*dir), os.ModePerm)
		if err != nil && !os.IsExist(err) {
			fmt.Fprintln(os.Stderr, "Error creating directory: ", err.Error())
			os.Exit(1)
		}
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error accepting connection: ", err.Error())
			continue
		}

		go handleConn(conn.(*net.TCPConn))
	}
}

func handleConn(conn *net.TCPConn) {

	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		contents := []string{}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error parsing request:", err.Error())
				return
			}
			if line == "\r\n" {
				break
			}
			contents = append(contents, line)
		}

		if len(contents) == 0 {
			return
		}

		fields := strings.Fields(contents[0]) // first row of curl request containing method, path and http version: POST /files/number HTTP/1.1
		if len(fields) < 3 {
			conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}

		method := fields[0]      // http method
		path := fields[1]        // requsted path
		httpVersion := fields[2] // http version

		acceptedEnconding := getHeader("Accept-Encoding", contents)
		shouldClose := strings.ToLower(getHeader("Connection", contents)) == "close"

		switch true {
		case path == "/":
			{
				conn.Write(fmt.Appendf(nil, "%s 200 OK\r\n\r\n", httpVersion))
				if shouldClose {
					// connection close header is present and should be returned
					conn.Write([]byte("Connection: close\r\n"))
				}
				break
			}
		case strings.HasPrefix(path, "/echo/"):
			{
				str := strings.Split(path, "/")[2] // idx 1 == echo, idx 2 == st
				var res []byte

				if strings.Contains(acceptedEnconding, "gzip") {
					gz, err := gzipString(str)
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error compressing string.")
						conn.Write(fmt.Appendf(nil, "%s 500 Internal Server Error\r\n\r\n", httpVersion))
					}
					res = gz
				} else {
					res = []byte(str)
				}

				conn.Write(fmt.Appendf(nil, "%s 200 OK\r\n", httpVersion))
				conn.Write([]byte("Content-Type: text/plain\r\n"))
				if acceptedEnconding != "" {
					conn.Write(fmt.Appendf(nil, "Content-Encoding: %s\r\n", acceptedEnconding))
				}
				if shouldClose {
					// connection close header is present and should be returned
					conn.Write([]byte("Connection: close\r\n"))
				}
				conn.Write(fmt.Appendf(nil, "Content-Length: %s\r\n\r\n", strconv.Itoa(len(res))))
				conn.Write(res)

				break
			}
		case strings.HasPrefix(path, "/user-agent"):
			{
				userAgent := getHeader("User-Agent", contents)
				if userAgent == "" {
					fmt.Fprintln(os.Stderr, "Missing user User-Agent.")
					conn.Write(fmt.Appendf(nil, "%s 400 Bad Request\r\n\r\n", httpVersion))
					break
				}

				conn.Write(fmt.Appendf(nil, "%s 200 OK\r\n", httpVersion))
				conn.Write([]byte("Content-Type: text/plain\r\n"))
				if acceptedEnconding != "" {
					conn.Write(fmt.Appendf(nil, "Content-Encoding: %s\r\n", acceptedEnconding))
				}
				if shouldClose {
					// connection close header is present and should be returned
					conn.Write([]byte("Connection: close\r\n"))
				}
				conn.Write(fmt.Appendf(nil, "Content-Length: %s\r\n\r\n", strconv.Itoa(len(userAgent))))
				conn.Write([]byte(userAgent))

				break
			}
		case strings.HasPrefix(path, "/files/"):
			{
				filename := strings.Split(path, "/")[2] // files at 1, {filename} at 2
				if method == "GET" {
					file, err := getFile(filename)
					if err != nil {
						if os.IsNotExist(err) {
							fmt.Fprintln(os.Stderr, "File not found")
							conn.Write(fmt.Appendf(nil, "%s 404 Not Found\r\n\r\n", httpVersion))
							break
						}
						fmt.Fprintln(os.Stderr, "Error reading file", err.Error())
						conn.Write(fmt.Appendf(nil, "%s 500 Internal Server Error\r\n\r\n", httpVersion))
						break
					}

					conn.Write(fmt.Appendf(nil, "%s 200 OK\r\n", httpVersion))
					conn.Write([]byte("Content-Type: application/octet-stream\r\n"))
					if acceptedEnconding != "" {
						conn.Write(fmt.Appendf(nil, "Content-Encoding: %s\r\n", acceptedEnconding))
					}
					if shouldClose {
						// connection close header is present and should be returned
						conn.Write([]byte("Connection: close\r\n"))
					}
					conn.Write(fmt.Appendf(nil, "Content-Length: %s\r\n\r\n", strconv.Itoa(len(file))))
					conn.Write(file)
				}

				if method == "POST" {
					i, found := slices.BinarySearchFunc(contents, "Content-Length", func(a, b string) int {
						if strings.Contains(a, b) {
							return 0
						}
						return -1
					})
					if !found {
						fmt.Fprintln(os.Stderr, "Missing content-length.")
						conn.Write(fmt.Appendf(nil, "%s 400 Bad Request\r\n\r\n", httpVersion))
						break
					}

					contentLength, err := strconv.Atoi(strings.Fields(contents[i])[1]) // content-length initally parsed like Content-Length: 5\r\n -> fields = Content-Length: ,5\r\n
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error parsing content-length:", err.Error())
						conn.Write(fmt.Appendf(nil, "%s 400 Bad Request\r\n\r\n", httpVersion))
						break
					}

					file := make([]byte, contentLength)
					reader.Read(file)

					err = postFile(filename, file)
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error posting file:", err.Error())
						conn.Write(fmt.Appendf(nil, "%s 500 Internal Server Error\r\n\r\n", httpVersion))
						break
					}
					conn.Write(fmt.Appendf(nil, "%s 201 Created\r\n\r\n", httpVersion))
					if shouldClose {
						// connection close header is present and should be returned
						conn.Write([]byte("Connection: close\r\n"))
					}
				}
				break
			}
		default:
			conn.Write(fmt.Appendf(nil, "%s 404 Not Found\r\n\r\n", httpVersion))
			if shouldClose {
				// connection close header is present and should be returned
				conn.Write([]byte("Connection: close\r\n"))
			}
		}

		if shouldClose {
			break
		}
	}
}

func getHeader(header string, contents []string) string {
	i, found := slices.BinarySearchFunc(contents, header, func(a, b string) int {
		if strings.Contains(a, b) {
			return 0
		}
		return -1
	})
	if !found {
		return ""
	}

	h := strings.TrimRight(contents[i], "\r\n")
	hValue := strings.TrimPrefix(h, string(fmt.Appendf(nil, "%s: ", header)))

	// for Content-Encoding validate and handle multiple values
	if header == "Accept-Encoding" {
		supported := []string{"gzip"}

		encodings := strings.Split(hValue, ", ")
		tmp := []string{}
		for _, en := range encodings {
			if slices.Contains(supported, strings.Trim(en, " ")) {
				tmp = append(tmp, en)
			}
		}
		hValue = strings.Join(tmp, ", ")
	}

	// otherwise return first value
	return hValue
}

func postFile(filename string, content []byte) error {
	file, err := os.Create(string(fmt.Appendf(nil, "%s/%s", string(*dir), filename)))
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(content)
	if err != nil {
		return err
	}

	return nil
}

func getFile(filename string) ([]byte, error) {
	file, err := os.ReadFile(string(fmt.Appendf(nil, "%s/%s", string(*dir), filename)))
	if err != nil {
		return nil, err
	}
	return file, nil
}

func gzipString(s string) ([]byte, error) {
	var b bytes.Buffer

	gz := gzip.NewWriter(&b)
	if _, err := gz.Write([]byte(s)); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
