package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"net"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// A struct to hold the data for a single pixel job
type pixelJob struct {
	x, y    int
	r, g, b uint8
}

func main() {
	fmt.Println("Starting Art Defense Bot...")
	fmt.Println("Press Ctrl+C to stop.")

	// --- Graceful Shutdown Setup ---
	// Create a context that is canceled when a SIGINT (Ctrl+C) or SIGTERM is received.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel() // Ensure cancel is called on exit to clean up resources

	// --- Run the bot for a single image ---
	imagePath := "./tobydio-square.jpg" // Defend this one image
	fmt.Printf("\n--- Painting and defending with %s ---\n", imagePath)

	// This function will block until the context is canceled (by Ctrl+C).
	err := sendAndDefendImage(ctx, imagePath)
	if err != nil && err != context.Canceled {
		fmt.Printf("Error during send/defend cycle: %v\n", err)
	}

	fmt.Println("\nShutdown complete.")
}

// sendAndDefendImage handles the entire lifecycle for one image:
// painting it once, then defending it until the context is canceled.
func sendAndDefendImage(ctx context.Context, imagePath string) error {
	conn, _, _, err := ws.Dial(ctx, "ws://34.16.209.13:3001/ws")
	if err != nil {
		return fmt.Errorf("error connecting: %w", err)
	}
	defer conn.Close()

	// This call relies on your existing loadImage function.
	placeImage, err := loadImage(imagePath)
	if err != nil {
		return err
	}

	// --- 1. Create an in-memory reference of the correct art ---
	artReference := make(map[image.Point]color.RGBA)
	bounds := placeImage.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := placeImage.At(x, y).RGBA()
			artReference[image.Point{x, y}] = color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
			}
		}
	}
	fmt.Println("Created in-memory art reference for defense.")

	var wg sync.WaitGroup
	var connMutex sync.Mutex
	jobs := make(chan pixelJob, 1000) // A healthy buffer for offense and defense jobs

	// --- 2. Start the consumer pool (the senders) ---
	numWorkers := runtime.NumCPU() * 6
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(&wg, &connMutex, conn, jobs)
	}

	// --- 3. Start the "Defense" reader goroutine ---
	go reader(ctx, conn, artReference, jobs)

	// --- 4. Run the initial "Offense" producer ---
	go func() {
		fmt.Println("Starting initial paint ('Offense')...")
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				correctColor := artReference[image.Point{x, y}]
				job := pixelJob{
					x: x, y: y,
					r: correctColor.R,
					g: correctColor.G,
					b: correctColor.B,
				}
				select {
				case jobs <- job:
				case <-ctx.Done():
					fmt.Println("Initial paint cancelled.")
					return
				}
			}
		}
		fmt.Println("Initial paint complete. Now in defense-only mode.")
	}()

	// --- 5. Wait for shutdown signal ---
	<-ctx.Done()

	// --- 6. Graceful Shutdown ---
	fmt.Println("Shutdown signal received. Closing workers...")
	close(jobs) // Signal workers to stop.
	wg.Wait()   // Wait for them to finish.
	fmt.Println("All workers have shut down.")
	return context.Canceled
}

// The new reader goroutine for our "Defense" system.
func reader(ctx context.Context, conn net.Conn, artReference map[image.Point]color.RGBA, jobs chan<- pixelJob) {
	for {
		msg, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			select {
			case <-ctx.Done(): // If the context is cancelled, this is an expected error.
			default:
				fmt.Printf("Reader error: %v\n", err)
			}
			return
		}

		if len(msg)%7 != 0 {
			continue // Ignore malformed messages.
		}

		for i := 0; i < len(msg); i += 7 {
			pixelBytes := msg[i : i+7]
			x := int(pixelBytes[0])<<8 | int(pixelBytes[1])
			y := int(pixelBytes[2])<<8 | int(pixelBytes[3])

			correctColor, ok := artReference[image.Point{x, y}]
			if !ok {
				continue // This pixel is outside our art, ignore it.
			}

			if pixelBytes[4] != correctColor.R || pixelBytes[5] != correctColor.G || pixelBytes[6] != correctColor.B {
				// fmt.Printf("DEFENDING pixel at (%d, %d). Reverting change.\n", x, y)
				defenseJob := pixelJob{
					x: x, y: y,
					r: correctColor.R,
					g: correctColor.G,
					b: correctColor.B,
				}
				select {
				case jobs <- defenseJob:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// The worker function is unchanged. It just sends whatever job it gets.
func worker(wg *sync.WaitGroup, connMutex *sync.Mutex, conn net.Conn, jobs <-chan pixelJob) {
	defer wg.Done()
	for job := range jobs {
		msg := []byte{
			byte(job.x >> 8), byte(job.x & 0xff),
			byte(job.y >> 8), byte(job.y & 0xff),
			job.r, job.g, job.b,
		}
		connMutex.Lock()
		err := wsutil.WriteClientBinary(conn, msg)
		connMutex.Unlock()
		if err != nil {
			if err != net.ErrClosed {
				fmt.Printf("Error sending message: %v\n", err)
			}
		}
	}
}
