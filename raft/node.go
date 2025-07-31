package raft

import (
	"math/rand"
	"net/rpc"
	"sync"
	"time"
)

func NewRaftNode(id string, peers []string) *RaftNode {
	node := &RaftNode{
		Id:          id,
		State:       Follower,
		CurrentTerm: 0,
		VotedFor:    "",
		Log:         []LogEntry{{Term: 0}}, // Genesis Entry
		CommitIndex: -1,
		LastApplied: -1,
		Peers:       peers,
		NextIndex:   make(map[string]int),
		MatchIndex:  make(map[string]int),
		VoteCh:      make(chan bool),
		AppendCh:    make(chan bool),
	}

	return node
}

func (n *RaftNode) ResetElectionTimer() {
	if n.ElectionTimer != nil {
		n.ElectionTimer.Stop()
	}
	// Random election timeout between 150-300ms
	timeout := time.Duration(150+rand.Intn(150)) * time.Millisecond
	n.ElectionTimer = time.AfterFunc(timeout, func() {
		n.startElection()
	})
}

func (n *RaftNode) startElection() {
	n.Lock()
	if n.State == Leader {
		n.Unlock()
		return
	}
	n.State = Candidate
	n.CurrentTerm++
	n.VotedFor = n.Id
	currentTerm := n.CurrentTerm

	// Prepare RequestVote rpc
	args := &RequestVoteArgs{
		Term:         currentTerm,
		CandidateId:  n.Id,
		LastLogIndex: len(n.Log) - 1,
		LastLogTerm:  -1,
	}
	if args.LastLogIndex >= 0 {
		args.LastLogTerm = n.Log[args.LastLogIndex].Term
	}
	n.Unlock()

	// Send RequestVote RPCs to all peers
	votes := 1 // Vote for self
	var voteMu sync.Mutex

	for _, peer := range n.Peers {
		if peer == n.Id {
			continue
		}
		go func(peer string) {
			var reply RequestVoteReply
			client, err := rpc.Dial("tcp", peer)
			if err != nil {
				return
			}
			defer client.Close()
			if err := client.Call("RaftNode.RequestVote", args, &reply); err != nil {
				return
			}
			n.Lock()
			if reply.Term > n.CurrentTerm {
				n.CurrentTerm = reply.Term
				n.State = Follower
				n.VotedFor = ""
				n.Unlock()
				return
			}
			n.Unlock()
			if reply.VoteGranted {
				voteMu.Lock()
				votes++
				if votes > len(n.Peers)/2 && n.State == Candidate {
					n.Lock()
					if n.State == Candidate {
						n.State = Leader
						for _, p := range n.Peers {
							n.NextIndex[p] = len(n.Log)
							n.MatchIndex[p] = -1
						}
						n.StartHeartbeat()
					}
					n.Unlock()
				}
				voteMu.Unlock()
			}
		}(peer)
	}
}

func (n *RaftNode) StartHeartbeat() {
	if n.HeartbeatTimer != nil {
		n.HeartbeatTimer.Stop()
	}
	n.HeartbeatTimer = time.AfterFunc(100*time.Millisecond, func() {
		n.sendHeartbeat()
		if n.State == Leader {
			n.StartHeartbeat()
		}
	})
}

func (n *RaftNode) sendHeartbeat() {
	n.Lock()
	if n.State != Leader {
		n.Unlock()
		return
	}
	for _, peer := range n.Peers {
		if peer == n.Id {
			continue
		}
		go func(peer string) {
			for {
				n.Lock()
				prevLogIndex := n.NextIndex[peer] - 1
				prevLogTerm := 0
				if prevLogIndex >= 0 && prevLogIndex < len(n.Log) {
					prevLogTerm = n.Log[prevLogIndex].Term
				}

				entries := []LogEntry{}
				if n.NextIndex[peer] < len(n.Log) {
					entries = n.Log[n.NextIndex[peer]:]
				}
				
				args := AppendEntriesArgs{
					Term:         n.CurrentTerm,
					LeaderId:     n.Id,
					PrevLogIndex: prevLogIndex,
					PrevLogTerm:  prevLogTerm,
					Entries:      entries,
					LeaderCommit: n.CommitIndex,
				}
				n.Unlock()
				var reply AppendEntriesReply
				client, err := rpc.Dial("tcp", peer)
				if err != nil {
					return
				}
				defer client.Close()
				if err := client.Call("RaftNode.AppendEntries", args, &reply); err != nil {
					return
				}
				n.Lock()
				if reply.Term > n.CurrentTerm {
					n.CurrentTerm = reply.Term
					n.State = Follower
					n.VotedFor = ""
					n.Unlock()
					return
				}
				if reply.Success {
					n.NextIndex[peer] = prevLogIndex + len(entries) + 1
					n.MatchIndex[peer] = n.NextIndex[peer] - 1
					n.Unlock()
					break
				} else {
					if n.NextIndex[peer] > 0 {
						n.NextIndex[peer]--
					}
					n.Unlock()
				}
			}
		}(peer)
	}
	n.Unlock()
}