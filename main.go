package main

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

var (
	// imagePath = "./tobydio-square.jpg"
	imagePath = "./miyab-square.png"
)

// A struct to hold the data for a single pixel job
type pixelJob struct {
	x, y    int
	r, g, b uint8
}

func main() {
	fmt.Println("Hello, World!")

	ctx := context.Background()
	conn, _, _, err := ws.Dial(ctx, "ws://34.16.209.13:3001/ws")
	if err != nil {
		fmt.Println("Error connecting:", err)
		return
	}
	defer conn.Close()

	placeImage, err := loadImage(imagePath)
	if err != nil {
		fmt.Println(err)
		return
	}

	// message structure:
	// byte 1-2: x position
	// byte 3-4: y position
	// byte 5: r
	// byte 6: g
	// byte 7: b
	// --- Concurrency Setup ---
	var wg sync.WaitGroup
	var connMutex sync.Mutex // The lock to protect the connection

	// A channel to act as a job queue for the workers
	jobs := make(chan pixelJob, 100)

	// Determine number of worker goroutines. A good starting point is the number of CPU cores.
	numWorkers := runtime.NumCPU() / 2
	fmt.Printf("Starting %d worker goroutines to send pixels...\n", numWorkers)

	// Start the worker pool
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(&wg, &connMutex, conn, jobs)
	}

	// --- Producer ---
	// The main goroutine will now be the "producer". It creates jobs
	// and puts them on the channel for the workers to pick up.
	fmt.Println("Producing pixel jobs...")
	bounds := placeImage.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, _ := placeImage.At(x, y).RGBA()
			jobs <- pixelJob{
				x: x, y: y,
				r: uint8(r >> 8),
				g: uint8(g >> 8),
				b: uint8(b >> 8),
			}
		}
	}
	close(jobs) // Close the channel to signal that no more jobs are coming.

	// --- Wait for Completion ---
	fmt.Println("All jobs produced. Waiting for workers to finish...")
	wg.Wait() // Wait for all goroutines in the WaitGroup to call Done()
	fmt.Println("All pixels sent successfully!")

}

// --- Worker Function ---
// This function is run by each of our concurrent goroutines.
func worker(wg *sync.WaitGroup, connMutex *sync.Mutex, conn net.Conn, jobs <-chan pixelJob) {
	defer wg.Done()

	for job := range jobs {
		msg := []byte{
			byte(job.x >> 8), byte(job.x & 0xff),
			byte(job.y >> 8), byte(job.y & 0xff),
			job.r, job.g, job.b,
		}

		// *** CRITICAL SECTION ***
		// Lock the mutex before writing to the connection to prevent data races.
		connMutex.Lock()

		err := wsutil.WriteClientBinary(conn, msg)

		// Unlock the mutex immediately after writing so another worker can proceed.
		connMutex.Unlock()
		// *** END CRITICAL SECTION ***

		if err != nil {
			// Note: In a real app, you might want a more robust error handling
			// strategy than just printing, as one error could cascade.
			fmt.Println("Error sending message:", err)
		}
	}
}
