package rpc

import (
	"fmt"
	"github.com/duanhf2012/originnet/log"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

type FuncRpcClient func(serviceMethod string) ([]*Client,error)
type FuncRpcServer func() (*Server)

type RpcMethodInfo struct {
	method reflect.Method
	iparam [] interface{}
	oParam reflect.Value
}

type RpcHandler struct {
	callRequest chan *RpcRequest
	rpcHandler IRpcHandler
	mapfunctons map[string]RpcMethodInfo
	funcRpcClient FuncRpcClient
	funcRpcServer FuncRpcServer
}

type IRpcHandler interface {
	GetName() string
	InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer)
	GetRpcHandler() IRpcHandler
	PushRequest(callinfo *RpcRequest)
	HandlerRpcRequest(request *RpcRequest)
	CallMethod(ServiceMethod string,reply interface{},param ...interface{}) error
}

func (slf *RpcHandler) GetRpcHandler() IRpcHandler{
	return slf.rpcHandler
}



func (slf *RpcHandler) InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer) {
	slf.callRequest = make(chan *RpcRequest,10000)

	slf.rpcHandler = rpcHandler
	slf.mapfunctons = map[string]RpcMethodInfo{}
	slf.funcRpcClient = getClientFun
	slf.funcRpcServer = getServerFun

	slf.RegisterRpc(rpcHandler)
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

// Is this type exported or a builtin?
func (slf *RpcHandler) isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}


func (slf *RpcHandler) suitableMethods(method reflect.Method) error {
	//只有RPC_开头的才能被调用
	if strings.Index(method.Name,"RPC_")!=0 {
		return nil
	}

	//取出输入参数类型
	var rpcMethodInfo RpcMethodInfo
	typ := method.Type
	if typ.NumOut() != 1 {
		return fmt.Errorf("%s The number of returned arguments must be 1!",method.Name)
	}

	if typ.Out(0).String() != "error" {
		return fmt.Errorf("%s The return parameter must be of type error!",method.Name)
	}

	for i := 1;i<typ.NumIn();i++{
		if slf.isExportedOrBuiltinType(typ.In(i)) == false {
			return fmt.Errorf("%s Unsupported parameter types!",method.Name)
		}

		//第一个参数为返回参数
		if i == 1 {
			rpcMethodInfo.oParam = reflect.New(typ.In(i).Elem())
		}else{
			rpcMethodInfo.iparam = append(rpcMethodInfo.iparam,reflect.New(typ.In(i).Elem()).Interface())
		}
	}

	rpcMethodInfo.method = method
	slf.mapfunctons[slf.rpcHandler.GetName()+"."+method.Name] = rpcMethodInfo
	return nil
}

func  (slf *RpcHandler) RegisterRpc(rpcHandler IRpcHandler) error {
	typ := reflect.TypeOf(rpcHandler)
	for m:=0;m<typ.NumMethod();m++{
		method := typ.Method(m)
		err := slf.suitableMethods(method)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

func (slf *RpcHandler) PushRequest(req *RpcRequest) {
	slf.callRequest <- req
}

func (slf *RpcHandler) GetRpcRequestChan() (chan *RpcRequest) {
	return slf.callRequest
}

func (slf *RpcHandler) Call(serviceMethod string,reply interface{},args ...interface{}) error {
	pClientList,err := slf.funcRpcClient(serviceMethod)
	if err != nil {
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if len(pClientList) > 1 {
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	pClient := pClientList[0]
	if pClient.blocalhost == true {
		pLocalRpcServer:=slf.funcRpcServer()
		//判断是否是同一服务
		sMethod := strings.Split(serviceMethod,".")
		if len(sMethod)!=2 {
			err := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
			log.Error("%+v",err)
			return err
		}
		//调用自己rpcHandler处理器
		if sMethod[0] == slf.rpcHandler.GetName() { //自己服务调用
			//
			return pLocalRpcServer.myselfRpcHandlerGo(sMethod[0],sMethod[1],reply,args...)
		}
		//其他的rpcHandler的处理器
		pCall := pLocalRpcServer.rpcHandlerGo(sMethod[0],sMethod[1],reply,args...)
		pResult := pCall.Done()
		return pResult.Err
	}

	//跨node调用
	pCall := pClient.Go(serviceMethod,reply,args...)
	pResult := pCall.Done()
	return pResult.Err
}


func (slf *RpcHandler) HandlerRpcRequest(request *RpcRequest) {
	v,ok := slf.mapfunctons[request.ServiceMethod]
	if ok == false {
		err := fmt.Errorf("RpcHandler %s cannot find %s",slf.rpcHandler.GetName(),request.ServiceMethod)
		log.Error("%s",err.Error())
		request.requestHandle(nil,err)
		return
	}

	var paramList []reflect.Value
	var err error
	if len(request.localParam)==0{
		err = processor.Unmarshal(request.InParam,&v.iparam)
		if err!=nil {
			rerr := fmt.Errorf("Call Rpc %s Param error %+v",request.ServiceMethod,err)
			log.Error("%s",rerr.Error())
			request.requestHandle(nil,rerr)
		}
	}else {
		v.iparam = request.localParam
	}


	paramList = append(paramList,reflect.ValueOf(slf.GetRpcHandler())) //接受者
	if request.localReply!=nil {
		paramList = append(paramList,reflect.ValueOf(request.localReply))
	}else{
		paramList = append(paramList,v.oParam) //输出参数
	}


	//其他输入参数
	for _,iv := range v.iparam {
		paramList = append(paramList,reflect.ValueOf(iv))
	}


	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	request.requestHandle(v.oParam.Interface(),err)
}

func (slf *RpcHandler) CallMethod(ServiceMethod string,reply interface{},param ...interface{}) error{
	var err error
	v,ok := slf.mapfunctons[ServiceMethod]
	if ok == false {
		err = fmt.Errorf("RpcHandler %s cannot find %s",slf.rpcHandler.GetName(),ServiceMethod)
		log.Error("%s",err.Error())

		return err
	}

	var paramList []reflect.Value
	paramList = append(paramList,reflect.ValueOf(slf.GetRpcHandler())) //接受者
	paramList = append(paramList,reflect.ValueOf(reply)) //输出参数

	//其他输入参数
	for _,iv := range param {
		paramList = append(paramList,reflect.ValueOf(iv))
	}


	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	return err
}