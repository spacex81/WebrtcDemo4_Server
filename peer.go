package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

type Peer struct {
	id     string
	myRoom *Room
	ws     *websocket.Conn
	pc     *webrtc.PeerConnection

	mu sync.RWMutex
}

func NewPeer(ws *websocket.Conn) *Peer {
	return &Peer{
		id: uuid.NewString(),
		ws: ws,
		mu: sync.RWMutex{},
	}
}

func (peer *Peer) connect() {
	// `mediaEngine` and `settingEngine` is used in order to send proper ice candidates to client
	mediaEngine := webrtc.MediaEngine{}
	mediaEngine.RegisterDefaultCodecs()

	settingEngine := webrtc.SettingEngine{}

	// settingEngine.SetEphemeralUDPPortRange(49152, 65535)
	publicIP := "13.124.110.77"
	settingEngine.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeSrflx)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithMediaEngine(&mediaEngine))

	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"}, // STUN server
			},
			{
				URLs:           []string{"turn:43.200.5.15:3478"}, // TURN server
				Username:       "testname",
				Credential:     "testpass",
				CredentialType: webrtc.ICECredentialTypePassword,
			},
		},
	})
	//

	// pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
	// 	ICEServers: []webrtc.ICEServer{
	// 		{
	// 			URLs: []string{"stun:stun.l.google.com:19302"}, // STUN server
	// 		},
	// 		{
	// 			URLs:           []string{"turn:43.200.5.15:3478"}, // TURN server
	// 			Username:       "testname",
	// 			Credential:     "testpass",
	// 			CredentialType: webrtc.ICECredentialTypePassword,
	// 		},
	// 	},
	// })

	if err != nil {
		log.Println(err)
	}
	peer.pc = pc

	// need to add tracks in order to send offer and candidates
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peer.pc.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			log.Print(err)
			return
		}
	}

	peer.pc.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		fmt.Println("OnICEConnectionStateChange")
		fmt.Println(is)
	})
	peer.pc.OnSignalingStateChange(func(ss webrtc.SignalingState) {
		// fmt.Println("OnSignalingStateChange")
		// fmt.Println(ss)
	})
	peer.pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		// fmt.Println("OnConnectionStateChange")
		// fmt.Println(pcs)
	})

	peer.pc.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i != nil {
			fmt.Println("\nOnICECandidate: ", i, "\n")
			iceBytes, err := json.Marshal(i.ToJSON())
			if err != nil {
				log.Println(err)
				return
			}
			messageBytes, err := json.Marshal(WebsocketMessage{
				Type: "candidate",
				Data: iceBytes,
			})
			if err != nil {
				log.Println(err)
				return
			}
			peer.safeWrite(messageBytes)

		}
	})

	// relay remote tracks received from a user to other room members
	peer.pc.OnTrack(func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
		trackLocal, err := webrtc.NewTrackLocalStaticRTP(tr.Codec().RTPCodecCapability, tr.ID(), tr.StreamID())
		if err != nil {
			log.Println("Error creating local track: ", err)
			return
		}

		room := peer.myRoom
		room.mu.Lock()
		room.trackLocals[trackLocal.ID()] = trackLocal
		room.mu.Unlock()

		defer peer.syncTracks()

		peer.syncTracks()
		buffer := make([]byte, 1500)
		for {
			n, _, err := tr.Read(buffer)
			if err != nil {
				log.Println("Error reading from trackRemote: ", err)
				break
			}

			if _, err := trackLocal.Write(buffer[:n]); err != nil {
				log.Println("Error writing to trackLocal: ", err)
				break
			}
		}
	})

	peer.sendOffer()
	// peer.syncTracks()
}

// this function should run whenever room.trackLocals is added/removed/created/destroyed
func (peer *Peer) syncTracks() {
	room := peer.myRoom
	room.mu.Lock()
	defer func() {
		room.mu.Unlock()
		room.dispatchKeyFrame()
	}()

	// 1. delete outdated tracks 2. prevent sending users own track 3. adding new tracks
	// return true for retry
	attemptSync := func() (tryAgain bool) {
		for _, peer := range room.peers {
			if peer.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				delete(room.peers, peer.id)
				return true
			}

			existingSenders := map[string]bool{}

			for _, sender := range peer.pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}

				existingSenders[sender.Track().ID()] = true

				// erase sender track from peer that is no longer need to be sent
				if _, ok := room.trackLocals[sender.Track().ID()]; !ok {
					if err := peer.pc.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			// need to add receiver tracks to the list in order to prevent
			// from sending tracks to client himself
			for _, receiver := range peer.pc.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// add tracks to client that is newly added to trackLocals
			for trackID, track := range room.trackLocals {
				if _, ok := existingSenders[trackID]; !ok {
					if _, err := peer.pc.AddTrack(track); err != nil {
						return true
					}
				}
			}

			// need to replace with 'peer.sendOffer'
			/////////////////////////////////////////////////
			offer, err := peer.pc.CreateOffer(nil)
			if err != nil {
				log.Println("Error on Creating Offer: ", err)
				return true
			}

			peer.pc.SetLocalDescription(offer)

			offerBytes, err := json.Marshal(offer)
			if err != nil {
				log.Println(err)
				return true
			}

			messageBytes, err := json.Marshal(WebsocketMessage{
				Type: "offer",
				Data: offerBytes,
			})
			if err != nil {
				log.Println(err)
				return true
			}

			peer.safeWrite(messageBytes)
			/////////////////////////////////////////////////
		}

		return
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			go func() {
				time.Sleep(time.Second * 3)
				peer.syncTracks()
			}()
			return
		}

		if !attemptSync() {
			break
		}
	}
}

func (peer *Peer) sendOffer() {
	offer, err := peer.pc.CreateOffer(nil)
	if err != nil {
		log.Println("Error on Creating Offer: ", err)
		return
	}

	peer.pc.SetLocalDescription(offer)

	offerBytes, err := json.Marshal(offer)
	if err != nil {
		log.Println(err)
		return
	}

	messageBytes, err := json.Marshal(WebsocketMessage{
		Type: "offer",
		Data: offerBytes,
	})
	if err != nil {
		log.Println(err)
		return
	}

	peer.safeWrite(messageBytes)
}

func (peer *Peer) sendRoomId() {
	roomIdBytes, err := json.Marshal(RoomIdData{
		RoomId: peer.myRoom.id,
	})
	if err != nil {
		log.Println(err)
		return
	}
	messageBytes, err := json.Marshal(WebsocketMessage{
		Type: "roomId",
		Data: roomIdBytes,
	})
	if err != nil {
		log.Println(err)
		return
	}
	peer.safeWrite(messageBytes)
}

func (peer *Peer) safeWrite(bytes []byte) {
	peer.mu.Lock()
	peer.ws.WriteMessage(websocket.TextMessage, bytes)
	peer.mu.Unlock()
}
