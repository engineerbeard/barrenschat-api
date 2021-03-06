package hub

import (
	"encoding/json"
	"log"

	"github.com/dbubel/barrenschat-api/config"
	"github.com/go-redis/redis"
)

type Hub struct {
	locker                chan bool
	clients               map[string]*Client     // Map of client IDs to *Client
	channelMembers        map[string][]*Client   // Map of channel names to []*Client
	topicChannels         map[string]chan []byte // Map of channel names to redis pubsub stream
	incomingClientMessags chan []byte
	clientConnect         chan *Client
	clientDisconnect      chan *Client
	msgRouter             map[string]func(rawMessage) // Map of message type to handler function

}

const (
	MessageTypeChat string = "message_new"
	MessageText     string = "message_text"

	CommandNewChannel    string = "message_new_channel"
	CommandNewChannelACK string = "message_new_channel_ACK"
)

var cmdMessages map[string]bool
var redisClient *redis.Client

func init() {
	cmdMessages = make(map[string]bool)
	cmdMessages[CommandNewChannel] = true

	redisClient = redis.NewClient(&redis.Options{
		Addr:     config.RedisURL,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
}

// NewHub used to create a new hub instance
func NewHub() *Hub {
	return &Hub{
		clients:               make(map[string]*Client),
		clientConnect:         make(chan *Client),
		clientDisconnect:      make(chan *Client), // todo: remove in favor of a message
		topicChannels:         make(map[string]chan []byte),
		channelMembers:        make(map[string][]*Client),
		incomingClientMessags: make(chan []byte),
		msgRouter:             make(map[string]func(rawMessage)),
		locker:                make(chan bool, 1),
	}
}

func (h *Hub) getClients() map[string]*Client {
	h.locker <- true
	c := h.clients
	<-h.locker
	return c
}

func (h *Hub) getTopicChannels() map[string]chan []byte {
	h.locker <- true
	c := h.topicChannels
	<-h.locker
	return c
}

func (h *Hub) newChannelListener(clientChannel string) {

	pSub := redisClient.Subscribe(clientChannel)
	cc := make(chan []byte)
	h.locker <- true
	h.topicChannels[clientChannel] = cc
	<-h.locker
	go func(c chan []byte, ps *redis.PubSub) {

		for {
			msg, err := ps.ReceiveMessage()
			if err != nil {
				log.Println("ERROR:", err.Error())
			}
			log.Println("REDIS RECV:", msg.Payload)
			var m rawMessage
			err = json.Unmarshal([]byte(msg.Payload), &m)
			if err != nil {
				log.Println("ERROR:", err.Error())
			}

			if handler, found := h.findHandler(m.MsgType); found {
				handler(m)
			} else {
				log.Println("WARN:", "No message type found")
			}
		}
	}(cc, pSub)
}

func (h *Hub) removeClient(client *Client) {
	h.locker <- true
	defer func() {
		<-h.locker
	}()
	delete(h.getClients(), client.getClientID())
	for _, channel := range client.channelsSubscribedTo {
		for i := range h.channelMembers[channel] {
			if client == h.channelMembers[channel][i] {
				copy(h.channelMembers[channel][i:], h.channelMembers[channel][i+1:])
				h.channelMembers[channel][len(h.channelMembers[channel])-1] = nil
				h.channelMembers[channel] = h.channelMembers[channel][:len(h.channelMembers[channel])-1]
				return
			}
		}
	}
}

func (h *Hub) addClient(client *Client) {
	for _, clientChannel := range client.channelsSubscribedTo {
		if _, ok := h.topicChannels[clientChannel]; !ok {
			h.newChannelListener(clientChannel)
		}
		h.locker <- true
		h.clients[client.getClientID()] = client
		h.channelMembers[clientChannel] = []*Client{}
		h.channelMembers[clientChannel] = append(h.channelMembers[clientChannel], client)
		<-h.locker
	}
}

// Run starts the hub listening on its channels
func (h *Hub) Run() {
	h.addHandler(MessageTypeChat, h.handleClientMessage)
	h.addHandler(CommandNewChannel, h.handleNewChannelCommand)

	for {
		select {
		case client := <-h.clientConnect:
			h.addClient(client)
		case client := <-h.clientDisconnect:
			h.removeClient(client)
		case message := <-h.incomingClientMessags:

			var m rawMessage
			err := json.Unmarshal(message, &m)
			if err != nil {
				log.Println(err.Error())
			}

			_, ok := cmdMessages[m.MsgType]
			if !ok {
				result := redisClient.Publish(m.Payload["channel"].(string), message)
				if result.Err() != nil {
					log.Println("ERROR", result.Err().Error())
				}
			} else {
				handler, found := h.findHandler(m.MsgType)
				if found {
					handler(m)
				}
			}
		}
	}
}
