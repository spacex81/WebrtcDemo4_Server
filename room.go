package main

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

type RoomManager struct {
	rooms map[string]*Room
	peers map[string]*Peer

	mu sync.RWMutex
}

func NewRoomManager() *RoomManager {
	return &RoomManager{
		rooms: map[string]*Room{},
		peers: map[string]*Peer{},
		mu:    sync.RWMutex{},
	}
}

func (manager *RoomManager) allocate() {
	fmt.Println("Length of Peers in RoomManager: ", len(manager.peers))
	fmt.Println("Length of Rooms in RoomManager: ", len(manager.rooms))
	if len(manager.peers) < 4 {
		return
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()

	room := NewRoom()
	manager.rooms[room.id] = room

	count := 0
	for peerID, peer := range manager.peers {
		room.peers[peerID] = peer
		peer.myRoom = room

		delete(manager.peers, peerID)

		count++

		if count == 4 {
			break
		}
	}

	// send room id to each clients
	for _, peer := range room.peers {
		peer.sendRoomId()
	}
	room.print()
	// establish peer connection among room.peers

	// initiate peer connection after room is created
	for _, peer := range room.peers {
		peer.connect()
	}
}

func (manager *RoomManager) addPeer(peer *Peer) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.peers[peer.id] = peer
}

type Room struct {
	id          string
	peers       map[string]*Peer
	trackLocals map[string]*webrtc.TrackLocalStaticRTP

	mu sync.RWMutex
}

func NewRoom() *Room {
	return &Room{
		id:          uuid.NewString(),
		peers:       map[string]*Peer{},
		trackLocals: map[string]*webrtc.TrackLocalStaticRTP{},
		mu:          sync.RWMutex{},
	}
}

func (room *Room) dispatchKeyFrame() {
	room.mu.Lock()
	defer room.mu.Unlock()

	for _, peer := range room.peers {
		for _, receiver := range peer.pc.GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			_ = peer.pc.WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}

func (room *Room) print() {
	fmt.Println("=============================Room=============================")
	fmt.Println("Room ID: ", room.id)
	for _, peer := range room.peers {
		fmt.Println("Peer ID: ", peer.id)
	}
	fmt.Println("==============================================================")
}
