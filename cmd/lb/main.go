package main

import (
	"load-balancer/lbpkg"
	"log"
	"net/http"
)

func main() {
	rr := lbpkg.NewRoundRobin(lbpkg.Servers)
	http.HandleFunc("/roundrobin", rr.Handler)

	wrr := lbpkg.NewWeightedRR(lbpkg.Servers, []int{1, 3, 2})
	http.HandleFunc("/weightedRR", wrr.Handler)

	lc := lbpkg.NewLeastConn(lbpkg.Servers)
	http.HandleFunc("/least_connection", lc.Handler)

	ch := lbpkg.NewConsistentHash(3)
	for _, s := range lbpkg.Servers {
		ch.Add(s)
	}
	http.HandleFunc("/consistent_hash", ch.Handler)

	go lbpkg.HealthChecker(rr, wrr, lc, ch)

	log.Println("Starting LB on localhost:8000")
	log.Fatal(http.ListenAndServe("localhost:8000", nil))
}
