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

var (
	wsUrl        = "ws://34.162.179.28:8001/ws"      // The WebSocket URL to connect to
	imagePath    = "./r-printedcircuitboard-2k.jpeg" // The path to the image we want to defend
	packetStruct = "old"                             // new or old, new is 16 bit x y, old is 32 bit x y
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
	conn, _, _, err := ws.Dial(ctx, wsUrl)
	if err != nil {
		return fmt.Errorf("error connecting: %w", err)
	}
	defer conn.Close()

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
	jobs := make(chan pixelJob, 1000) // A healthy buffer for offense and defense jobs

	// --- 2. Start the consumer pool (the senders) ---
	numWorkers := runtime.NumCPU()
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(&wg, ctx, jobs)
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

		if packetStruct == "new" && len(msg) < 7 {
			fmt.Println("Received malformed message, expected at least 7 bytes for new packet struct.")
			continue // Ignore malformed messages.
		}

		if packetStruct == "old" && len(msg) < 11 {
			fmt.Println("Received malformed message, expected at least 11 bytes for old packet struct.")
			continue // Ignore malformed messages.
		}

		if packetStruct == "new" {
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
		if packetStruct == "old" {
			for i := 0; i < len(msg); i += 11 {
				pixelBytes := msg[i : i+11]
				x := int(pixelBytes[0])<<24 | int(pixelBytes[1])<<16 | int(pixelBytes[2])<<8 | int(pixelBytes[3])
				y := int(pixelBytes[4])<<24 | int(pixelBytes[5])<<16 | int(pixelBytes[6])<<8 | int(pixelBytes[7])

				correctColor, ok := artReference[image.Point{x, y}]
				if !ok {
					continue // This pixel is outside our art, ignore it.
				}

				if pixelBytes[8] != correctColor.R || pixelBytes[9] != correctColor.G || pixelBytes[10] != correctColor.B {
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
}

// The worker function is unchanged. It just sends whatever job it gets.
func worker(wg *sync.WaitGroup, ctx context.Context, jobs <-chan pixelJob) {
	conn, _, _, err := ws.Dial(ctx, wsUrl)
	if err != nil {
		fmt.Printf("Error connecting in worker: %v\n", err)
		return
	}
	defer conn.Close()

	defer wg.Done()
	for job := range jobs {
		var msg []byte

		if packetStruct == "new" {
			msg = []byte{
				byte(job.x >> 8), byte(job.x & 0xff),
				byte(job.y >> 8), byte(job.y & 0xff),
				job.r, job.g, job.b,
			}
		}
		if packetStruct == "old" {
			msg = []byte{
				byte(job.x >> 24), byte((job.x >> 16) & 0xff),
				byte((job.x >> 8) & 0xff), byte(job.x & 0xff),
				byte(job.y >> 24), byte((job.y >> 16) & 0xff),
				byte((job.y >> 8) & 0xff), byte(job.y & 0xff),
				job.r, job.g, job.b,
			}
		}
		err := wsutil.WriteClientBinary(conn, msg)
		if err != nil {
			if err != net.ErrClosed {
				fmt.Printf("Error sending message: %v\n", err)
			}
		}
	}
}
