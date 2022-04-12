package recorder

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
)

var Capacity int = 10 // capacity of each subscriber
var subscribers sync.Map

type option struct {
	recordChan     chan Record
	transport      string
	targetPort     string
	includePayload bool
}

type Record struct {
	Timestamp  string
	UserHash   string
	ClientIp   string
	ClientPort string
	TargetHost string
	TargetPort string
	Transport  string
	Payload    []byte
}

func Add(hash string, clientAddr, targetAddr net.Addr, transport string, payload []byte) {
	clientIP, clientPort, _ := net.SplitHostPort(clientAddr.String())
	targetHost, targetPort, _ := net.SplitHostPort(targetAddr.String())

	record := Record{
		Timestamp:  strconv.Itoa(int(time.Now().UnixMilli())),
		UserHash:   hash,
		ClientIp:   clientIP,
		ClientPort: clientPort,
		TargetHost: targetHost,
		TargetPort: targetPort,
		Transport:  transport,
		Payload:    payload,
	}
	broadcast(record)
}

func Subscribe(uid string, transport, targetPort string, includePayload bool) chan Record {
	log.Debug("New recorder subscriber", uid)
	opt := option{
		recordChan:     make(chan Record, Capacity),
		transport:      transport,
		targetPort:     targetPort,
		includePayload: includePayload,
	}
	subscribers.Store(uid, opt)
	return opt.recordChan
}

func Unsubscribe(uid string) {
	log.Debug("Delete recorder subscriber", uid)
	subscribers.Delete(uid)
}

func broadcast(record Record) {
	payload := record.Payload

	subscribers.Range(func(uid, o any) bool {
		opt := o.(option)
		if opt.transport != "" && opt.transport != record.Transport {
			return true
		}
		if opt.targetPort != "" && opt.targetPort != record.TargetPort {
			return true
		}
		if opt.includePayload {
			buf := make([]byte, len(payload))
			copy(buf, payload)
			record.Payload = buf
		} else {
			record.Payload = nil
		}

		select {
		case opt.recordChan <- record:
		default:
		}
		return true
	})
}
