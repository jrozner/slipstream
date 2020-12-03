package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"sync"
	"text/template"
)

const sipResponse = "SIP/2.0 200 OK\r\n" +
	"{{ .Via }};received=0.0.0.0\r\n" +
	"From: <sip:wuzzi@example.org;transport=TCP>;tag=U7c3d519\r\n" +
	"To: <sip:wuzzi@example.org;transport=TCP>;tag=37GkEhwl6\r\n" +
	"Call-ID: aaaaaaaaaaaaaaaaa0404aaaaaaaaaaaabbbbbbZjQ4M2M.\r\n" +
	"CSeq: 1 REGISTER\r\n" +
	"{{ .Contact }};expires=3600\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n"

const sipRequest = "REGISTER sip:example.org;transport=TCP SIP/2.0\r\n" +
	"Via: SIP/2.0/TCP {{ .LocalIP }}:{{ .RemotePort }};branch=I9hG4bK-d8754z-c2ac7de1b3ce90f7-1---d8754z-;rport;transport=TCP\r\n" +
	"Max-Forwards: 70\r\n" +
	"Contact: <sip:wuzzi@{{ .LocalIP }}:{{ .LocalPort }};rinstance=v40f3f83b335139c;transport=TCP>\r\n" +
	"To: <sip:wuzzi@example.org;transport=TCP>\r\n" +
	"From: <sip:wuzzi@example.org;transport=TCP>;tag=U7c3d519\r\n" +
	"Call-ID: aaaaaaaaaaaaaaaaa0404aaaaaaaaaaaabbbbbbZjQ4M2M.\r\n" +
	"CSeq: 1 REGISTER\r\n" +
	"Expires: 60\r\n" +
	"Allow: REGISTER, INVITE, ACK, CANCEL, BYE, NOTIFY, REFER, MESSAGE, OPTIONS, INFO, SUBSCRIBE\r\n" +
	"Supported: replaces, norefersub, extended-refer, timer, X-cisco-serviceuri\r\n" +
	"Allow-Events: presence, kpml\r\n" +
	"Content-Length: 0\r\n" +
	"\r\n"

var extractContact = regexp.MustCompile(`Contact:[^\r]+`)
var extractVia = regexp.MustCompile(`Via:[^\r]+`)
var extractCallback = regexp.MustCompile(`@(?P<callback>[^;]+)`)

func startSIPServer(sipPort string) error {
	t := template.Must(template.New("sip_response").Parse(sipResponse))
	l, err := net.Listen("tcp", ":"+sipPort)
	if err != nil {
		return err
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Println("unable to accept connection: ", err)
			continue
		}

		fmt.Printf("accepted connection from: %s\n", conn.RemoteAddr())

		go func(c net.Conn, t *template.Template) {
			defer c.Close()
			// TODO: we can probably switch this over to a bufio to make it more efficient
			data := make([]byte, 0, 1024)
			for {
				ch := make([]byte, 1)
				_, err = c.Read(ch)
				if err != nil {
					log.Println("unable to read: ", err)
					return
				}

				data = append(data, ch...)

				// TODO: swap out this comparison with bytes.Compare() to avoid the generation of a string
				ds := string(data)
				len := len(ds)
				if len > 3 {
					if ds[len-4:len] == "\r\n\r\n" {
						break
					}
				}
			}

			contact := extractContact.Find(data)
			if len(contact) < 1 {
				log.Println("bad contact")
				return
			}

			via := extractVia.Find(data)
			if len(via) < 1 {
				log.Println("bad via")
				return
			}

			vars := struct {
				Via     string
				Contact string
			}{
				Via:     string(via),
				Contact: string(contact),
			}

			var buff bytes.Buffer

			err = t.Execute(&buff, vars)
			if err != nil {
				// TODO: failed to write template, this should never happen
			}

			c.Write(buff.Bytes())
			if err != nil {
				log.Println("error sending response: ", err)
			}

			matches := extractCallback.FindSubmatch(contact)

			if len(matches) < 2 {
				// TODO: handle bad match
			}

			fmt.Printf("connecting back to: %s\n", string(matches[1]))
			c2, err := net.Dial("tcp", string(matches[1]))
			if err != nil {
				//  TODO: handle error
			}

			defer c2.Close()
			c2.Write([]byte("hello world!\n"))
		}(conn, t)
	}
}

func setupListener(port string, wg *sync.WaitGroup) {
	defer wg.Done()
	// NOTE: listening on :<port> ends up breaking when testing this in WSL
	// doing port forwarding I assume due to WSL attempting to bind to
	// Window's interface and failing and the forwarding getting confused.
	// Disabling the forwarding doesn't actually fix it.
	ln, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatal("unable to open socket for listening: ", err)
	}

	defer ln.Close()

	fmt.Println("listening on ", port)

	conn, err := ln.Accept()
	if err != nil {
		log.Fatal("unable to accept incomming connect: ", err)
	}

	defer conn.Close()

	fmt.Println("accepted conection from ", conn.RemoteAddr().String())

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Println("unable to read from connection: ", err)
	}

	fmt.Printf("received message from remote server: `%s`\n", line)
}

func sendRequest(host, localIP, localPort, remotePort string) error {
	t := template.Must(template.New("sip_request").Parse(sipRequest))

	vars := struct {
		LocalIP    string
		LocalPort  string
		RemotePort string
	}{
		LocalIP:    localIP,
		LocalPort:  localPort,
		RemotePort: remotePort,
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", host, remotePort))
	if err != nil {
		return err
	}

	var buff bytes.Buffer
	err = t.Execute(&buff, vars)
	if err != nil {
		return err
	}

	_, err = conn.Write(buff.Bytes())
	if err != nil {
		return err
	}

	return nil
}

func main() {
	var (
		remotePort string
		localPort  string
		localIP    string
		host       string
		listen     bool
	)

	flag.StringVar(&localPort, "lp", "", "the port to listen on locally (server and client)")
	flag.StringVar(&remotePort, "rp", "", "the port to connect to (client)")
	flag.StringVar(&localIP, "ip", "", "the local NAT ip to connect back to")
	flag.StringVar(&host, "host", "", "the host to connect to")
	flag.BoolVar(&listen, "l", false, "listen for incoming connections; this makes it a server")

	flag.Parse()

	if listen {
		if localPort == "" {
			fmt.Fprintf(os.Stderr, "you must specify a local port\n")
			flag.Usage()
			os.Exit(1)
		}

		err := startSIPServer(localPort)
		if err != nil {
			log.Fatal("unable to start SIP sever", err)
		}
	} else {
		if localPort == "" {
			fmt.Fprintf(os.Stderr, "you must specify a local port\n")
			flag.Usage()
			os.Exit(1)
		}

		if remotePort == "" {
			fmt.Fprintf(os.Stderr, "you must specify a remote port\n")
			flag.Usage()
			os.Exit(1)
		}

		if localIP == "" {
			fmt.Fprintf(os.Stderr, "you must specify a local ip address\n")
			flag.Usage()
			os.Exit(1)
		}

		if host == "" {
			fmt.Fprintf(os.Stderr, "you must specify a host\n")
			flag.Usage()
			os.Exit(1)
		}

		wg := sync.WaitGroup{}
		wg.Add(1)
		go setupListener(localPort, &wg)

		err := sendRequest(host, localIP, localPort, remotePort)
		if err != nil {
			log.Fatal(err)
		}

		wg.Wait()
	}
}
