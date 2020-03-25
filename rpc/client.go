package rpc

import (
	"fmt"
	"github.com/duanhf2012/originnet/log"
	"github.com/duanhf2012/originnet/network"
	"math"
	"sync"
	"time"
)
//go:generate msgp
type Client struct {
	blocalhost bool
	network.TCPClient
	conn *network.TCPConn

	//
	pendingLock sync.RWMutex
	startSeq uint64
	pending map[uint64]*Call
}

func (slf *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	slf.conn = conn
	return slf
}

func (slf *Client) Connect(addr string) error {
	slf.Addr = addr

	/*
	if addr == "" || strings.Index(addr,"localhost")!=-1 {
		slf.blocalhost = true
		return nil
	}*/

	slf.ConnNum = 1
	slf.ConnectInterval = time.Second*2
	slf.PendingWriteNum = 10000
	slf.AutoReconnect = true
	slf.LenMsgLen =2
	slf.MinMsgLen = 2
	slf.MaxMsgLen = math.MaxUint16
	slf.NewAgent =slf.NewClientAgent
	slf.LittleEndian = LittleEndian

	slf.pendingLock.Lock()
	for _,v := range slf.pending {
		v.Err = fmt.Errorf("node is disconnect.")
		v.done <- v
	}
	slf.pending = map[uint64]*Call{}
	slf.pendingLock.Unlock()
	slf.Start()
	return nil
}

func (slf *Client) Go(serviceMethod string,reply interface{}, args ...interface{}) *Call {
	call := new(Call)
	call.done = make(chan *Call,1)
	call.Reply = reply

	request := &RpcRequest{}
	call.Arg = args
	slf.pendingLock.Lock()
	slf.startSeq += 1
	call.Seq = slf.startSeq
	request.Seq = slf.startSeq
	slf.pending[call.Seq] = call
	slf.pendingLock.Unlock()

	request.ServiceMethod = serviceMethod
	var herr error
	request.InParam,herr = processor.Marshal(args)
	if herr != nil {
		call.Err = herr
		return call
	}

	bytes,err := processor.Marshal(request)
	if err != nil {
		call.Err = err
		return call
	}
	fmt.Print(string(bytes))

	err = slf.conn.WriteMsg(bytes)
	if err != nil {
		call.Err = err
	}

	return call
}

type RequestHandler func(Returns interface{},Err error)


type RpcRequest struct {
	//packhead
	Seq uint64// sequence number chosen by client
	ServiceMethod string   // format: "Service.Method"

	//packbody
	InParam []byte
	requestHandle RequestHandler
}

type RpcResponse struct {
	//head
	Seq           uint64   // sequence number chosen by client
	Err error

	//returns
	Returns []byte
}


func (slf *Client) Run(){
	for {
		bytes,err := slf.conn.ReadMsg()
		if err != nil {
			slf.Close()
			slf.Start()
		}
		//1.解析head
		respone := &RpcResponse{}
		err = processor.Unmarshal(bytes,respone)
		if err != nil {
			log.Error("rpcClient Unmarshal head error,error:%+v",err)
			continue
		}

		slf.pendingLock.Lock()
		v,ok := slf.pending[respone.Seq]
		if ok == false {
			log.Error("rpcClient cannot find seq %d in pending",respone.Seq)
			slf.pendingLock.Unlock()
		}else  {
			delete(slf.pending,respone.Seq)
			slf.pendingLock.Unlock()

			err = processor.Unmarshal(respone.Returns,v.Reply)
			if err != nil {
				log.Error("rpcClient Unmarshal body error,error:%+v",err)
				continue
			}

			//发送至接受者
			v.done <- v
		}


	}

}

func (slf *Client) OnClose(){
	//关闭时，重新连接
	slf.Start()
}