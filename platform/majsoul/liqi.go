package majsoul

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sync"
	"time"
	"github.com/EndlessCheng/mahjong-helper/platform/majsoul/proto/lq"
	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
)

const (
	messageTypeNotify   = 1
	messageTypeRequest  = 2
	messageTypeResponse = 3
)

type WebSocketClient struct {
	sync.Mutex

	ws     *websocket.Conn
	closed bool

	messageIndex       uint16
	respMessageChanMap *sync.Map // messageIndex -> chan proto.Message
}

func NewWebSocketClient() *WebSocketClient {
	return &WebSocketClient{
		respMessageChanMap: &sync.Map{},
	}
}

func (*WebSocketClient) WrapMessage(name string, message proto.Message) (data []byte, err error) {
	data, err = proto.Marshal(message)
	if err != nil {
		return
	}
	wrap := lq.Wrapper{
		Name: name,
		Data: data,
	}
	return proto.Marshal(&wrap)
}

func (*WebSocketClient) UnwrapData(rawData []byte) (methodName string, data []byte, err error) {
	wrapper := lq.Wrapper{}
	if err = proto.Unmarshal(rawData, &wrapper); err != nil {
		return
	}
	return wrapper.GetName(), wrapper.GetData(), nil
}

// TODO: auto UnwrapMessage by methodName

func (c *WebSocketClient) UnwrapMessage(rawData []byte, message proto.Message) error {
	methodName, data, err := c.UnwrapData(rawData)
	if err != nil {
		return err
	}
	// TODO: assert methodName
	_ = methodName
	return proto.Unmarshal(data, message)
}

func (c *WebSocketClient) run() {
	for !c.closed {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if c.closed {
				return
			}
			fmt.Fprintln(os.Stderr, "ws.ReadMessage:", err)
			continue
		}

		if len(data) <= 3 {
			fmt.Fprintln(os.Stderr, "数据过短", data)
			continue
		}

		if data[0] == messageTypeResponse {
			messageIndex := binary.LittleEndian.Uint16(data[1:3])
			rawRespMessageChan, ok := c.respMessageChanMap.Load(messageIndex)
			if !ok {
				fmt.Fprintln(os.Stderr, "未找到消息", messageIndex)
				continue
			}
			c.respMessageChanMap.Delete(messageIndex)

			respMessageType := reflect.TypeOf(rawRespMessageChan).Elem().Elem()
			respMessage := reflect.New(respMessageType)
			if err := c.UnwrapMessage(data[3:], respMessage.Interface().(proto.Message)); err != nil {
				fmt.Fprintln(os.Stderr, "UnwrapData:", err)
				reflect.ValueOf(rawRespMessageChan).Close()
				continue
			}
			reflect.ValueOf(rawRespMessageChan).Send(respMessage)
		}
	}
}

func (c *WebSocketClient) Connect(endpoint string, origin string) error {
	header := http.Header{}
	header.Set("origin", origin) // 模拟来源
	ws, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	if err != nil {
		return err
	}
	c.ws = ws

	go c.run()
	go c.heartbeat()

	return nil
}

func (c *WebSocketClient) Close() error {
	c.closed = true
	return c.ws.Close()
}

func (c *WebSocketClient) send(name string, reqMessage proto.Message, respMessageChan interface{}) error {
	c.Lock()
	defer c.Unlock()

	data, err := c.WrapMessage(name, reqMessage)
	if err != nil {
		return err
	}

	c.messageIndex = (c.messageIndex + 1) % 60007 // from code.js

	messageIndexBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(messageIndexBytes, c.messageIndex)
	messageHead := append([]byte{messageTypeRequest}, messageIndexBytes...)
	if err := c.ws.WriteMessage(websocket.BinaryMessage, append(messageHead, data...)); err != nil {
		return err
	}

	c.respMessageChanMap.Store(c.messageIndex, respMessageChan)
	return nil
}

func (c *WebSocketClient) callFastTest(methodName string, reqMessage proto.Message, respMessageChan interface{}) error {
	return c.send(".lq.FastTest."+methodName, reqMessage, respMessageChan)
}

func (c *WebSocketClient) callLobby(methodName string, reqMessage proto.Message, respMessageChan interface{}) error {
	return c.send(".lq.Lobby."+methodName, reqMessage, respMessageChan)
}

func (c *WebSocketClient) heartbeat() {
	for !c.closed {
		// 吐槽：雀魂的开发把 heart 错写成了 heat
		if _, err := c.Heatbeat(&lq.ReqHeatBeat{}); err != nil {
			fmt.Fprintln(os.Stderr, "heartbeat:", err)
		}
		time.Sleep(6 * time.Second)
	}
}
