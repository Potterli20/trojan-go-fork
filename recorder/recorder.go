package recorder

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/p4gefau1t/trojan-go/log"
)

var Capacity int = 10 // capacity of each subscriber
var subscribers sync.Map

type Record struct {
	Timestamp  string
	UserHash   string
	ClientIp   string
	ClientPort string
	TargetHost string
	TargetPort string
	Transport  string
}

func Add(hash string, clientAddr, targetAddr net.Addr, transport string) {
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
	}
	broadcast(record)
}

func Subscribe(uid string) chan Record {
	rc := make(chan Record, Capacity)
	log.Debug("New recorder subscriber", uid)
	subscribers.Store(uid, rc)
	return rc
}

func Unsubscribe(uid string) {
	log.Debug("Delete recorder subscriber", uid)
	subscribers.Delete(uid)
}

func broadcast(record Record) {
	subscribers.Range(func(uuid, rc interface{}) bool {
		c := rc.(chan Record)
		select {
		case c <- record:
		default:
		}
		return true
	})
}
