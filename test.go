package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"load-balancer/lbpkg"
	"math/rand"
	"net"
	"net/http"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
)

func red(s string) string    { return colorRed + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }

// Simulation helpers
func simulateRoundRobin() {
	fmt.Println("== Round Robin ==")
	rr := lbpkg.NewRoundRobin(lbpkg.Servers)
	half := 6
	before := make([]string, half)
	after := make([]string, half)
	for i := 0; i < half; i++ {
		s := rr.Next()
		if s == "" {
			before[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		if err != nil {
			before[i] = red("ERR")
		} else {
			before[i] = labelFromURL(s)
		}
	}
	// down S2
	fmt.Printf("%s %s\n", red("[down]"), labelFromURL(lbpkg.Servers[1]))
	rr.RemoveServer(lbpkg.Servers[1])
	for i := 0; i < half; i++ {
		s := rr.Next()
		if s == "" {
			after[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		if err != nil {
			after[i] = red("ERR")
		} else {
			after[i] = labelFromURL(s)
		}
	}

	fmt.Println("Req  | Before | After  | Change")
	for i := 0; i < half; i++ {
		change := ""
		if before[i] != after[i] {
			change = yellow("changed")
		}
		fmt.Printf("%3d  | %6s | %6s | %s\n", i+1, before[i], after[i], change)
	}
	fmt.Println()
}

func simulateWeighted() {
	fmt.Println("== Weighted Round Robin ==")
	wrr := lbpkg.NewWeightedRR(lbpkg.Servers, []int{1, 3, 2})
	half := 6
	before := make([]string, half)
	after := make([]string, half)
	for i := 0; i < half; i++ {
		s := wrr.Next()
		if s == "" {
			before[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		if err != nil {
			before[i] = red("ERR")
		} else {
			before[i] = labelFromURL(s)
		}
	}
	fmt.Printf("%s %s\n", red("[down]"), labelFromURL(lbpkg.Servers[1]))
	wrr.RemoveServer(lbpkg.Servers[1])
	for i := range half {
		s := wrr.Next()
		if s == "" {
			after[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		if err != nil {
			after[i] = red("ERR")
		} else {
			after[i] = labelFromURL(s)
		}
	}
	fmt.Println("Req  | Before | After  | Change")
	for i := range half {
		change := ""
		if before[i] != after[i] {
			change = yellow("changed")
		}
		fmt.Printf("%3d  | %6s | %6s | %s\n", i+1, before[i], after[i], change)
	}
	fmt.Println()
}

func simulateLeastConnections() {
	fmt.Println("== Least Connections ==")
	lc := lbpkg.NewLeastConn(lbpkg.Servers)
	half := 6
	before := make([]string, half)
	after := make([]string, half)
	beforeActive := make([]int, half)
	afterActive := make([]int, half)
	for i := range half {
		s := lc.Next()
		if s == "" {
			before[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		active := 0
		for idx, url := range lc.Servers {
			if url == s {
				active = lc.ActiveConnections[idx]
				break
			}
		}
		if err != nil {
			before[i] = red("ERR")
		} else {
			before[i] = labelFromURL(s)
		}
		beforeActive[i] = active
	}
	fmt.Printf("%s %s\n", red("[down]"), labelFromURL(lbpkg.Servers[1]))
	lc.RemoveServer(lbpkg.Servers[1])
	for i := range half {
		s := lc.Next()
		if s == "" {
			after[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", s, "home"))
		active := 0
		for idx, url := range lc.Servers {
			if url == s {
				active = lc.ActiveConnections[idx]
				break
			}
		}
		if err != nil {
			after[i] = red("ERR")
		} else {
			after[i] = labelFromURL(s)
		}
		afterActive[i] = active
	}
	fmt.Println("Req  | Before | aConns | After  | aConns | Change")
	for i := range half {
		change := ""
		if before[i] != after[i] {
			change = yellow("changed")
		}
		fmt.Printf("%3d  | %6s | %6d | %6s | %6d | %s\n", i+1, before[i], beforeActive[i], after[i], afterActive[i], change)
	}
	fmt.Println()
}

func randomIP() net.IP {
	return net.IPv4(byte(rand.Intn(256)), byte(rand.Intn(256)), byte(rand.Intn(256)), byte(rand.Intn(256)))
}

func randomPort() int {
	return rand.Intn(65535-1024) + 1024
}

func simulateConsistentHashing() {
	fmt.Println("== Consistent Hashing (cache + health check) ==")
	ch := lbpkg.NewConsistentHash(64)
	for _, s := range lbpkg.Servers {
		ch.Add(s)
	}
	totalClients := 100
	clients := []string{}
	for range totalClients {
		ip, port := randomIP(), randomPort()
		clients = append(clients, fmt.Sprintf("%s:%d", ip.String(), port))
	}

	fnv32a := func(s string) uint32 { h := fnv.New32a(); h.Write([]byte(s)); return h.Sum32() }
	before := make([]string, totalClients)
	after := make([]string, totalClients)
	for i, c := range clients {
		srv := ch.Get(fnv32a(c))
		if srv == "" {
			before[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", srv, "home"))
		if err != nil {
			before[i] = red("ERR")
		} else {
			before[i] = labelFromURL(srv)
		}
	}
	fmt.Printf("%s %s\n", red("[down]"), labelFromURL(lbpkg.Servers[1]))
	ch.Remove(lbpkg.Servers[1])
	for i, c := range clients {
		srv := ch.Get(fnv32a(c))
		if srv == "" {
			after[i] = red("ERR")
			continue
		}
		_, err := doGet(fmt.Sprintf("%s/%s", srv, "home"))
		if err != nil {
			after[i] = red("ERR")
		} else {
			after[i] = labelFromURL(srv)
		}
	}
	fmt.Printf("Client        \t\t| Before | After  | Remapped\n")
	remapped := 0
	for i, c := range clients {
		change := ""
		if before[i] != after[i] {
			change = yellow("yes")
			remapped++
		}
		fmt.Printf("%-13s \t| %6s | %6s | %s\n", c, before[i], after[i], change)
	}
	fmt.Printf("Remapped clients: %d / %d\n", remapped, totalClients)
	fmt.Println()
}

func labelFromURL(url string) string {
	for i, u := range lbpkg.Servers {
		if u == url {
			return fmt.Sprintf("S%d", i+1)
		}
	}
	return url
}

func main() {
	live := flag.Bool("live", false, "call live LB endpoints at localhost:8000 instead of simulating")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

	if *live {
		fmt.Println("Live mode: calling endpoints on http://localhost:8000. Make sure lb server is running.")
		simulateRoundRobinLive()
		simulateWeightedLive()
		simulateLeastConnectionsLive()
		simulateConsistentHashingLive()
		return
	}

	// simulated mode (default)
	simulateRoundRobin()
	simulateWeighted()
	simulateLeastConnections()
	simulateConsistentHashing()
}

// --- Live mode helpers (call the LB endpoints) ---
func doGet(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)
	return string(b), nil
}

func simulateRoundRobinLive() {
	fmt.Println("== Round Robin (live) ==")
	total := 12
	for i := 1; i <= total; i++ {
		url := fmt.Sprintf("http://localhost:8000/roundrobin?path=home")
		body, err := doGet(url)
		if err != nil {
			fmt.Printf("Req %2d -> %s %v\n", i, red("error"), err)
			continue
		}
		fmt.Printf("Req %2d -> %s\n", i, body)
	}
	fmt.Println()
}

func simulateWeightedLive() {
	fmt.Println("== Weighted Round Robin (live) ==")
	total := 12
	for i := 1; i <= total; i++ {
		url := fmt.Sprintf("http://localhost:8000/weightedRR?path=home")
		body, err := doGet(url)
		if err != nil {
			fmt.Printf("Req %2d -> %s %v\n", i, red("error"), err)
			continue
		}
		fmt.Printf("Req %2d -> %s\n", i, body)
	}
	fmt.Println()
}

func simulateLeastConnectionsLive() {
	fmt.Println("== Least Connections (live) ==")
	total := 12
	for i := 1; i <= total; i++ {
		url := fmt.Sprintf("http://localhost:8000/least_connection?path=home")
		body, err := doGet(url)
		if err != nil {
			fmt.Printf("Req %2d -> %s %v\n", i, red("error"), err)
			continue
		}
		fmt.Printf("Req %2d -> %s\n", i, body)
	}
	fmt.Println()
}

func simulateConsistentHashingLive() {
	fmt.Println("== Consistent Hashing (live) ==")
	total := 12
	for i := 1; i <= total; i++ {
		url := fmt.Sprintf("http://localhost:8000/consistent_hash?path=home")
		body, err := doGet(url)
		if err != nil {
			fmt.Printf("Req %2d -> %s %v\n", i, red("error"), err)
			continue
		}
		fmt.Printf("Req %2d -> %s\n", i, body)
	}
	fmt.Println()
}
