package h2quicsim

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/lucas-clemente/quic-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Client is the interface for the simulation client
type Client interface {
	Run(string) error
	Wait()
}

type client struct {
	db      []Entry
	connMap map[int][]*Entry
	fin     map[string]chan struct{}

	st time.Time
}

// NewClient returns a client with db entries
func NewClient(db []Entry) (Client, error) {
	ret := &client{
		db:      db,
		connMap: make(map[int][]*Entry),
		fin:     make(map[string]chan struct{}),
	}

	for idx, ent := range db {
		url := ent.URL
		conn := ent.Connection
		ret.connMap[conn] = append(ret.connMap[conn], &db[idx])
		ret.fin[url] = make(chan struct{})
	}

	return ret, nil
}

func (cl *client) Run(addr string) error {
	cl.st = time.Now()
	for _, ents := range cl.connMap {
		sess, err := quic.DialAddr(addr,
			&tls.Config{InsecureSkipVerify: true},
			&quic.Config{CreatePaths: true},
		)
		if err != nil {
			return err
		}

		go cl.handleSess(sess, ents)
	}
	return nil
}

func (cl *client) Wait() {
	for _, fi := range cl.fin {
		<-fi
	}
}

func (cl *client) handleSess(sess quic.Session, ents []*Entry) {
	defer sess.Close(nil)
	conn := &clientConn{cl: cl, ents: ents, sess: sess}
	err := conn.run()
	if err != nil {
		log.Println(err)
	}
}

type clientConn struct {
	cl     *client
	ents   []*Entry
	entMap map[int]*Entry

	sess   quic.Session
	header quic.Stream

	reqs chan *Entry

	st     time.Time
	stTime map[int]time.Time
}

func (c *clientConn) handleData(data quic.Stream, size int, url string) {
	buf := make([]byte, size)
	for n, err := data.Read(buf); n != 0 && err != io.EOF; n, err = data.Read(buf) {
		if err != nil {
			log.Println(err)
		}
	}

	sid := int(data.StreamID())
	st := c.stTime[sid]
	cu := time.Now()
	log.Printf("stream %v finish", sid)
	fmt.Println(sid, msec(st, cu), msec(c.st, cu), msec(c.cl.st, cu))
	close(c.cl.fin[url])
}

func (c *clientConn) handleHeader() {
	h2framer := http2.NewFramer(nil, c.header)
	for {
		frame, err := h2framer.ReadFrame()
		if err != nil {
			log.Println(err)
			return
		}
		headerFrame, ok := frame.(*http2.HeadersFrame)
		if !ok {
			log.Println("header frame error")
			continue
		}

		sid := int(headerFrame.StreamID)
		ent := c.entMap[sid]
		size := ent.Size

		log.Printf("stream %v size %v", sid, size)
		data, _ := c.sess.(streamCreator).GetOrOpenStream(quic.StreamID(sid))
		go c.handleData(data, size, ent.URL)
	}
}

func (c *clientConn) doRequest() {
	h2framer := http2.NewFramer(c.header, nil)

	for ent := range c.reqs {
		c.stTime[ent.Stream] = time.Now()

		var headers bytes.Buffer
		h2pack := hpack.NewEncoder(&headers)
		for _, h := range ent.Request {
			h2pack.WriteField(h)
		}

		h2framer.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      uint32(ent.Stream),
			BlockFragment: headers.Bytes(),
			EndStream:     true,
			EndHeaders:    true,
			Priority: http2.PriorityParam{
				StreamDep: uint32(ent.Dependency),
				Weight:    uint8(ent.Weight - 1),
			},
		})
	}
}

func (c *clientConn) run() error {
	header, err := c.sess.OpenStreamSync()
	if err != nil {
		return err
	}
	c.header = header

	c.st = time.Now()
	c.stTime = make(map[int]time.Time)
	c.entMap = make(map[int]*Entry)
	for _, ent := range c.ents {
		str, err := c.sess.OpenStreamSync()
		if err != nil {
			return err
		}
		str.Close()
		sid := int(str.StreamID())
		if sid != ent.Stream {
			return ErrDB
		}
		c.entMap[sid] = ent
	}

	var wg sync.WaitGroup
	go c.doRequest()
	for _, ent := range c.ents {
		wg.Add(1)
		go func(e *Entry) {
			pa := e.Initiator
			if fi, ok := c.cl.fin[pa]; ok {
				<-fi
			}
			c.reqs <- e
			wg.Done()
		}(ent)
	}

	wg.Wait()
	close(c.reqs)
	return nil
}
