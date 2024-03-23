package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

type WebsocketMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type RoomIdData struct {
	RoomId string `json:"roomId"`
}

type CandidateData struct {
	Candidate        string `json:"candidate"`
	SDPMid           string `json:"sdpMid"`
	SDPMLineIndex    uint16 `json:"sdpMLineIndex"`
	UsernameFragment string `json:"usernameFragment"`
}

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	manager  = NewRoomManager()
)

func main() {
	http.HandleFunc("/", healthCheckHandler)
	http.HandleFunc("/api/websocket", wsHandler)

	go func() {
		// time.Sleep(time.Second * 30)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				manager.allocate()
			}
		}
	}()

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error Listening to Server: ", err)
	}
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error Upgrading Websocket: ", err)
		return
	}

	peer := NewPeer(ws)
	manager.addPeer(peer)

	defer func() {
		// remove peer from manager.peers or room.peers
		// close ws, close pc
	}()

	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			log.Println("Error Reading Websocket Message")
			break
		}

		var message WebsocketMessage
		if err := json.Unmarshal(p, &message); err != nil {
			log.Println("Error unmarshaling websocket message")
			break
		}

		switch message.Type {
		// Only server sends SDP offer
		// Don't need to write listener for "offer"
		case "answer":
			var answer webrtc.SessionDescription
			json.Unmarshal(message.Data, &answer)
			if peer.pc.SignalingState() == webrtc.SignalingStateHaveLocalOffer {
				if err := peer.pc.SetRemoteDescription(answer); err != nil {
					log.Println("Error SetRemoteDescription(answer): ", err)
					break
				}
			}
		case "candidate":
			candidateData := CandidateData{}
			json.Unmarshal(message.Data, &candidateData)
			fmt.Println("Ice Candidate Received: ")
			fmt.Println(candidateData, "\n")

			// Now, create the ICECandidateInit object from candidateData.
			candidate := webrtc.ICECandidateInit{
				Candidate: candidateData.Candidate,
				SDPMid:    &candidateData.SDPMid, // Take the address to get a pointer
			}

			// For optional fields like SDPMLineIndex and UsernameFragment,
			// you need to ensure they're appropriately handled as pointers.
			var sdpMLineIndex *uint16 = nil
			if candidateData.SDPMLineIndex != 0 { // Assuming 0 is not a valid value and signifies "unset".
				sdpMLineIndex = new(uint16)
				*sdpMLineIndex = candidateData.SDPMLineIndex
			}

			candidate.SDPMLineIndex = sdpMLineIndex

			// UsernameFragment is already a string so it can be directly assigned if not empty.
			var usernameFragment *string = nil
			if candidateData.UsernameFragment != "" {
				usernameFragment = &candidateData.UsernameFragment
			}

			candidate.UsernameFragment = usernameFragment

			if err := peer.pc.AddICECandidate(candidate); err != nil {
				log.Println(err)
				break
			}
		}
	}
}
