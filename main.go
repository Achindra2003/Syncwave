package main

import (
	"fmt"
	"net/http"
	"syncwave/network"
)

func main() {
	hub := network.NewHub()

	// Serve frontend
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	// WebSocket endpoint
	http.HandleFunc("/ws", hub.ServeWS)

	fmt.Println("╔═══════════════════════════════════════════╗")
	fmt.Println("║   SyncWave: Collaborative Editor Server   ║")
	fmt.Println("╠═══════════════════════════════════════════╣")
	fmt.Println("║  Open http://localhost:8080 in browser     ║")
	fmt.Println("║  Open multiple tabs to collaborate!        ║")
	fmt.Println("╚═══════════════════════════════════════════╝")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("Server error:", err)
	}
}
