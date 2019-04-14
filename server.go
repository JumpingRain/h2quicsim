package h2quicsim

import (
	"bytes"
	"crypto/tls"
	"errors"
	"log"
	"sync"

	"github.com/lucas-clemente/quic-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Server is the interface for the simulation server
type Server interface {
	Listen(addr string, certFile string, privFile string) error
}

type server struct {
	db     []Entry
	objMap map[string]*Entry
}

// NewServer returns a server of an website db
func NewServer(db []Entry) (Server, error) {
	ret := &server{db: db, objMap: make(map[string]*Entry)}
	for idx, ent := range db {
		enc := encodeObject(ent.Request)
		if _, ok := ret.objMap[enc]; ok {
			// we ignore duplicate request
			log.Printf("duplicate request %v", enc)
			// continue
			return nil, ErrDB
		}
		ret.objMap[enc] = &db[idx]
	}
	return ret, nil
}

func (s *server) Listen(addr string, certFile string, privFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, privFile)
	if err != nil {
		return err
	}

	listener, err := quic.ListenAddr(addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil)
	if err != nil {
		return err
	}

	defer listener.Close()
	for {
		sess, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.handleSess(sess)
	}
}

func (s *server) handleSess(sess quic.Session) {
	defer sess.Close(nil)
	conn := &serverConn{serv: s, sess: sess}
	err := conn.run()
	if err != nil {
		log.Println(err)
	}
}

type serverConn struct {
	serv *server

	sess       quic.Session
	header     quic.Stream
	headerLock sync.Mutex

	h2framer *http2.Framer
	h2pack   *hpack.Decoder
}

type streamCreator interface {
	GetOrOpenStream(quic.StreamID) (quic.Stream, error)
}

type remoteCloser interface {
	CloseRemote(quic.ByteCount)
}

// returns true if close the connection
func (c *serverConn) acceptHeader() (bool, error) {
	frame, err := c.h2framer.ReadFrame()
	if err != nil {
		return false, err
	}
	headerFrame, ok := frame.(*http2.HeadersFrame)
	if !ok || !headerFrame.HeadersEnded() {
		return true, errors.New("not header frame")
	}
	headers, err := c.h2pack.DecodeFull(headerFrame.HeaderBlockFragment())
	if err != nil {
		return true, err
	}

	enc := encodeObject(headers)
	sid := quic.StreamID(headerFrame.StreamID)
	stream, err := c.sess.(streamCreator).GetOrOpenStream(sid)
	if err != nil {
		return true, err
	}
	stream.SetPriority(quic.StreamID(headerFrame.Priority.StreamDep), int(headerFrame.Priority.Weight)+1)

	log.Printf("stream %v handle %v", sid, enc)
	go c.handleData(stream, enc)
	return true, nil
}

func (c *serverConn) handleData(stream quic.Stream, enc string) {
	defer stream.Close()
	stream.(remoteCloser).CloseRemote(0)
	stream.Read([]byte{0})

	sid := stream.StreamID()
	db := c.serv.objMap
	if resp, ok := db[enc]; ok {
		var headers bytes.Buffer
		h2pack := hpack.NewEncoder(&headers)

		for _, h := range resp.Response {
			h2pack.WriteField(h)
		}

		c.headerLock.Lock()
		framer := http2.NewFramer(c.header, nil)
		err := framer.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      uint32(stream.StreamID()),
			EndHeaders:    true,
			BlockFragment: headers.Bytes(),
		})
		c.headerLock.Unlock()
		if err != nil {
			log.Println(err)
		}

		length := resp.Size
		if length <= 0 {
			length = 1
		}
		log.Printf("response %v with %v bytes", sid, length)
		n, err := stream.Write(make([]byte, length))
		if err != nil {
			log.Println(err)
		}
		log.Printf("finish %v with %v bytes", sid, n)
	} else {
		log.Printf("response %v not found, ignore", sid)
	}
}

func (c *serverConn) run() error {
	header, err := c.sess.AcceptStream()
	if err != nil {
		return err
	}
	header.SetPriority(0, 256)
	c.header = header

	c.h2framer = http2.NewFramer(nil, header)
	c.h2pack = hpack.NewDecoder(4096, nil)

	for {
		ct, err := c.acceptHeader()
		if err != nil {
			log.Println(err)
		}
		if !ct {
			return err
		}
	}
}
