package lbpkg

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	murmur3 "github.com/spaolacci/murmur3"
)

var Servers = []string{
	"http://localhost:8080",
	"http://localhost:8081",
	"http://localhost:8082",
}

type RoundRobin struct {
	Servers                []string
	PerviousServer         int
	NumberOfServers        int
	NumbersOfRequestHandle int
	Mu                     sync.Mutex
}

func NewRoundRobin(servers []string) *RoundRobin {
	return &RoundRobin{Servers: servers, PerviousServer: -1, NumberOfServers: len(servers)}
}

func (rr *RoundRobin) RemoveServer(server string) {
	rr.Mu.Lock()
	defer rr.Mu.Unlock()
	for i, s := range rr.Servers {
		if s == server {
			rr.Servers = append(rr.Servers[:i], rr.Servers[i+1:]...)
			return
		}
	}
}

func (rr *RoundRobin) Handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	rr.Mu.Lock()
	idx := (rr.PerviousServer + 1) % len(rr.Servers)
	serverAddr := rr.Servers[idx]
	rr.PerviousServer = idx
	rr.Mu.Unlock()
	rr.NumbersOfRequestHandle += 1

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/%s", serverAddr, path), nil)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		w.Write(fmt.Appendf(nil, "error from the server # %d: %s", idx, err))
		return
	}

	body, _ := io.ReadAll(resp.Body)
	w.Write([]byte(body))
}

// Next returns the next server according to round-robin and updates counters.
func (rr *RoundRobin) Next() string {
	rr.Mu.Lock()
	if len(rr.Servers) == 0 {
		rr.Mu.Unlock()
		return ""
	}
	idx := (rr.PerviousServer + 1) % len(rr.Servers)
	serverAddr := rr.Servers[idx]
	rr.PerviousServer = idx
	rr.Mu.Unlock()
	rr.NumbersOfRequestHandle += 1
	return serverAddr
}

// Weighted Round Robin
type WeightedRR struct {
	Servers       []string
	Weights       []int
	CurrentIndex  int
	CurrentWeight int
	MaxWeight     int
	GcdWeight     int
	Mu            sync.Mutex
}

func NewWeightedRR(servers []string, weights []int) *WeightedRR {
	max := 0
	for _, w := range weights {
		if w > max {
			max = w
		}
	}
	gcd := 0
	if len(weights) > 0 {
		gcd = weights[0]
		for _, w := range weights[1:] {
			gcd = gcdSlice([]int{gcd, w})
		}
		if gcd == 0 {
			gcd = 1
		}
	} else {
		gcd = 1
	}
	return &WeightedRR{Servers: servers, Weights: weights, CurrentIndex: -1, MaxWeight: max, GcdWeight: gcd}
}

func (wrr *WeightedRR) RemoveServer(server string) {
	wrr.Mu.Lock()
	defer wrr.Mu.Unlock()
	for i, s := range wrr.Servers {
		if s == server {
			wrr.Servers = append(wrr.Servers[:i], wrr.Servers[i+1:]...)
			return
		}
	}
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func gcdSlice(nums []int) int {
	g := nums[0]
	for _, n := range nums[1:] {
		g = gcd(g, n)
	}
	return g
}

func (wrr *WeightedRR) nextServer() string {
	wrr.Mu.Lock()
	defer wrr.Mu.Unlock()

	for {
		wrr.CurrentIndex = (wrr.CurrentIndex + 1) % len(wrr.Servers)
		if wrr.CurrentIndex == 0 {
			wrr.CurrentWeight -= wrr.GcdWeight
			if wrr.CurrentWeight <= 0 {
				wrr.CurrentWeight = wrr.MaxWeight
				if wrr.CurrentWeight == 0 {
					return wrr.Servers[0]
				}
			}
		}
		if wrr.Weights[wrr.CurrentIndex] >= wrr.CurrentWeight {
			return wrr.Servers[wrr.CurrentIndex]
		}
	}
}

func (wrr *WeightedRR) Handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	serverAddr := wrr.nextServer()
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/%s", serverAddr, path), nil)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		w.Write(fmt.Appendf(nil, "error from %s: %v", serverAddr, err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	w.Write(body)
}

// Next returns the next server according to weighted round-robin.
func (wrr *WeightedRR) Next() string {
	return wrr.nextServer()
}

// Least Connections
type LeastConn struct {
	Servers           []string
	NumberOfServers   int
	ActiveConnections []int
	Mu                sync.Mutex
}

func NewLeastConn(servers []string) *LeastConn {
	return &LeastConn{Servers: servers, NumberOfServers: len(servers), ActiveConnections: make([]int, len(servers))}
}

func (lc *LeastConn) RemoveServer(server string) {
	lc.Mu.Lock()
	defer lc.Mu.Unlock()
	for i, s := range lc.Servers {
		if s == server {
			lc.Servers = append(lc.Servers[:i], lc.Servers[i+1:]...)
			return
		}
	}
}

func (lc *LeastConn) Handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	lc.Mu.Lock()
	minIndex := 0
	minConn := lc.ActiveConnections[0]
	for i := 1; i < len(lc.Servers); i++ {
		if lc.ActiveConnections[i] < minConn {
			minConn = lc.ActiveConnections[i]
			minIndex = i
		}
	}
	lc.ActiveConnections[minIndex] += 1
	lc.Mu.Unlock()

	serverAddr := lc.Servers[minIndex]
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/%s", serverAddr, path), nil)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)

	lc.Mu.Lock()
	lc.ActiveConnections[minIndex] -= 1
	lc.Mu.Unlock()

	if err != nil {
		w.Write(fmt.Appendf(nil, "error from %s: %v", serverAddr, err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	w.Write(body)
}

// Next selects the server with least active connections and increments its counter.
func (lc *LeastConn) Next() string {
	lc.Mu.Lock()
	if len(lc.Servers) == 0 {
		lc.Mu.Unlock()
		return ""
	}
	minIndex := 0
	minConn := lc.ActiveConnections[0]
	for i := 1; i < len(lc.Servers); i++ {
		if lc.ActiveConnections[i] < minConn {
			minConn = lc.ActiveConnections[i]
			minIndex = i
		}
	}
	lc.ActiveConnections[minIndex] += 1
	server := lc.Servers[minIndex]
	lc.Mu.Unlock()
	return server
}

// Release decrements the active connection count for the given server.
func (lc *LeastConn) Release(server string) {
	lc.Mu.Lock()
	defer lc.Mu.Unlock()
	for i, s := range lc.Servers {
		if s == server {
			if lc.ActiveConnections[i] > 0 {
				lc.ActiveConnections[i] -= 1
			}
			return
		}
	}
}

// Consistent Hash
type hashRange struct {
	start uint32
	end   uint32
}

type Cache struct {
	Table map[uint32]string
	Mu    sync.RWMutex
}

func (c *Cache) InvalidateAffected(ranges []hashRange) {
	c.Mu.Lock()
	defer c.Mu.Unlock()

	for key := range c.Table {
		for _, r := range ranges {
			if r.start < r.end {
				if key > r.start && key <= r.end {
					delete(c.Table, key)
					break
				} else {
					if key > r.start || key <= r.end {
						delete(c.Table, key)
						break
					}
				}
			}
		}
	}
}

func hashKey(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

type ConsistentHash struct {
	Replicas int
	Keys     []uint32
	Ring     map[uint32]string
	Mu       sync.RWMutex
	Alive    map[string]bool
	Cache    *Cache
}

func NewConsistentHash(replicas int) *ConsistentHash {
	return &ConsistentHash{
		Replicas: replicas,
		Ring:     make(map[uint32]string),
		Alive:    make(map[string]bool),
		Cache: &Cache{
			Table: make(map[uint32]string),
		},
	}
}

func (ch *ConsistentHash) Add(server string) {
	ch.Mu.Lock()

	affected := []hashRange{}
	for i := 0; i < ch.Replicas; i++ {
		h := hashKey(fmt.Sprintf("%s#%d", server, i))

		idx := sort.Search(len(ch.Keys), func(i int) bool { return ch.Keys[i] >= h })
		ch.Keys = append(ch.Keys, 0)
		copy(ch.Keys[idx+1:], ch.Keys[idx:])
		ch.Keys[idx] = h
		ch.Ring[h] = server

		idx = sort.Search(len(ch.Keys), func(i int) bool { return ch.Keys[i] >= h })
		prevIdx := (idx - 1 + len(ch.Keys)) % len(ch.Keys)
		affected = append(affected, hashRange{start: ch.Keys[prevIdx], end: h})
	}
	ch.Alive[server] = true
	ch.Mu.Unlock()

	ch.Cache.InvalidateAffected(affected)
}

func (ch *ConsistentHash) Remove(server string) {
	ch.Mu.Lock()

	ch.Alive[server] = false
	affected := []hashRange{}
	for i := 0; i < ch.Replicas; i++ {
		h := hashKey(fmt.Sprintf("%s#%d", server, i))
		delete(ch.Ring, h)

		idx := sort.Search(len(ch.Keys), func(i int) bool { return ch.Keys[i] >= h })
		if idx < len(ch.Keys) && ch.Keys[idx] == h {
			prevIdx := (idx - 1 + len(ch.Keys)) % len(ch.Keys)
			nextIdx := (idx + 1) % len(ch.Keys)
			affected = append(affected, hashRange{start: ch.Keys[prevIdx], end: ch.Keys[nextIdx]})
			ch.Keys = append(ch.Keys[:idx], ch.Keys[idx+1:]...)
		}
	}
	ch.Mu.Unlock()

	ch.Cache.InvalidateAffected(affected)
}

func (ch *ConsistentHash) hash5TupleStable(srcIP net.IP, srcPort int, dstIP net.IP, dstPort int, protocol uint8) uint32 {
	h := murmur3.New32()
	src := srcIP.To16()
	dst := dstIP.To16()

	h.Write(src)
	h.Write(dst)
	var b [5]byte
	binary.BigEndian.PutUint16(b[0:2], uint16(srcPort))
	binary.BigEndian.PutUint16(b[2:4], uint16(dstPort))
	b[4] = protocol
	h.Write(b[:5])

	return h.Sum32()
}

func (ch *ConsistentHash) Get(h uint32) string {
	ch.Cache.Mu.RLock()
	server, ok := ch.Cache.Table[h]
	ch.Cache.Mu.RUnlock()

	if ok && ch.Alive[server] {
		return server
	}

	ch.Mu.RLock()
	defer ch.Mu.RUnlock()

	if len(ch.Keys) == 0 {
		return ""
	}

	idx := sort.Search(len(ch.Keys), func(i int) bool { return ch.Keys[i] >= h })
	idx = idx % len(ch.Keys)
	return ch.Ring[ch.Keys[idx]]
}

func (ch *ConsistentHash) Handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	dstAddr, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	srcHost, srcPortStr, err1 := net.SplitHostPort(r.RemoteAddr)
	dstHost, dstPortStr, err2 := net.SplitHostPort(dstAddr.String())
	var hv uint32
	if err1 == nil && err2 == nil {
		srcIP := net.ParseIP(srcHost)
		dstIP := net.ParseIP(dstHost)
		srcPort, _ := strconv.Atoi(srcPortStr)
		dstPort, _ := strconv.Atoi(dstPortStr)
		hv = ch.hash5TupleStable(srcIP, srcPort, dstIP, dstPort, 6)
	} else {
		hv = hashKey(r.RemoteAddr + "->" + dstAddr.String())
	}
	server := ch.Get(hv)
	if server == "" {
		w.Write([]byte("no servers available"))
		return
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/%s", server, path), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error from %s: %v, removing...", server, err)
		ch.Remove(server)

		server = ch.Get(hv)
		if server == "" {
			w.Write([]byte("no fallback servers available"))
			return
		}
		log.Println("Retrying with:", server)

		req, _ = http.NewRequest("GET", fmt.Sprintf("%s/%s", server, path), nil)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			w.Write([]byte(fmt.Sprintf("error from %s: %v", server, err)))
			return
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Write(body)
}

func HealthChecker(rr *RoundRobin, wrr *WeightedRR, lc *LeastConn, ch *ConsistentHash) {
	alive := []bool{true, true, true}
	var wg sync.WaitGroup
	for {
		wg.Add(3)
		go func() {
			defer wg.Done()

			server := Servers[0]
			req, _ := http.NewRequest("GET", fmt.Sprintf("%s/health", server), nil)
			resp, err := http.DefaultClient.Do(req)
			if (err != nil || resp.StatusCode != 200) && alive[0] {
				ch.Remove(server)
				rr.RemoveServer(server)
				wrr.RemoveServer(server)
				lc.RemoveServer(server)
				alive[0] = false
				log.Printf("%s is down", server)
				return
			}
			if resp.StatusCode == 200 && !alive[0] {
				ch.Add(server)
				rr.Servers = append(rr.Servers, server)
				wrr.Servers = append(wrr.Servers, server)
				lc.Servers = append(lc.Servers, server)
				alive[0] = true
				log.Printf("%s is up", server)
			}
		}()

		go func() {
			defer wg.Done()

			server := Servers[1]
			req, _ := http.NewRequest("GET", fmt.Sprintf("%s/health", server), nil)
			resp, err := http.DefaultClient.Do(req)
			if (err != nil || resp.StatusCode != 200) && alive[1] {
				ch.Remove(server)
				rr.RemoveServer(server)
				wrr.RemoveServer(server)
				lc.RemoveServer(server)
				alive[1] = false
				log.Printf("%s is down", server)
				return
			}
			if resp.StatusCode == 200 && !alive[1] {
				ch.Add(server)
				rr.Servers = append(rr.Servers, server)
				wrr.Servers = append(wrr.Servers, server)
				lc.Servers = append(lc.Servers, server)
				alive[1] = true
				log.Printf("%s is up", server)
			}
		}()

		go func() {
			defer wg.Done()

			server := Servers[2]
			req, _ := http.NewRequest("GET", fmt.Sprintf("%s/health", server), nil)
			resp, err := http.DefaultClient.Do(req)
			if (err != nil || resp.StatusCode != 200) && alive[2] {
				ch.Remove(server)
				rr.RemoveServer(server)
				wrr.RemoveServer(server)
				lc.RemoveServer(server)
				alive[2] = false
				log.Printf("%s is down", server)
				return
			}
			if resp.StatusCode == 200 && !alive[2] {
				ch.Add(server)
				rr.Servers = append(rr.Servers, server)
				wrr.Servers = append(wrr.Servers, server)
				lc.Servers = append(lc.Servers, server)
				alive[2] = true
				log.Printf("%s is up", server)
			}
		}()

		wg.Wait()

		time.Sleep(3 * time.Minute)
	}
}
