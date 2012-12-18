// Copyright 2012 Google, Inc. All rights reserved.

// +build ignore

// This benchmark reads in file <tempdir>/gopacket_benchmark.pcap and measures
// the time it takes to decode all packets from that file.  If the file doesn't
// exist, it's pulled down from a publicly available location.  However, you can
// feel free to substitute your own file at that location, in which case the
// benchmark will run on your own data.
//
// It's also useful for figuring out which packets may be causing errors.  Pass
// in the --printErrors flag, and it'll print out error layers for each packet
// that has them.  This includes any packets that it's just unable to decode,
// which is a great way to find new protocols to decode, and get test packets to
// write tests for them.
package main

import (
	"compress/gzip"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/gconnell/gopacket"
	"github.com/gconnell/gopacket/pcap"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"runtime/pprof"
	"time"
)

var decodeLazy *bool = flag.Bool("lazy", false, "If true, use lazy decoding")
var decodeNoCopy *bool = flag.Bool("nocopy", false, "If true, avoid an extra copy when decoding packets")
var printErrors *bool = flag.Bool("printErrors", false, "If true, check for and print error layers.")
var printLayers *bool = flag.Bool("printLayers", false, "If true, print out the layers of each packet")
var repeat *int = flag.Int("repeat", 10, "Read over the file N times")
var cpuProfile *string = flag.String("cpuprofile", "", "If set, write CPU profile to filename")
var url *string = flag.String("url", "http://www.ll.mit.edu/mission/communications/cyber/CSTcorpora/ideval/data/1999/training/week1/tuesday/inside.tcpdump.gz", "URL to gzip'd pcap file")

type BufferPacketSource struct {
	index int
	data  [][]byte
	ci    []gopacket.CaptureInfo
}

func NewBufferPacketSource(p gopacket.PacketDataSource) *BufferPacketSource {
	start := time.Now()
	b := &BufferPacketSource{}
	for {
		data, ci, err := p.ReadPacketData()
		if err == io.EOF {
			break
		}
		b.data = append(b.data, data)
		b.ci = append(b.ci, ci)
	}
	duration := time.Since(start)
	fmt.Printf("Reading packet data into memory: %d packets in %v, %v per packet\n", len(b.data), duration, duration/time.Duration(len(b.data)))
	return b
}

func (b *BufferPacketSource) ReadPacketData() (data []byte, ci gopacket.CaptureInfo, err error) {
	if b.index >= len(b.data) {
		err = io.EOF
		return
	}
	data = b.data[b.index]
	ci = b.ci[b.index]
	b.index++
	return
}

func main() {
	flag.Parse()
	filename := os.TempDir() + string(os.PathSeparator) + "gopacket_benchmark.pcap"
	if _, err := os.Stat(filename); err != nil {
		// This URL points to a publicly available packet data set from a DARPA
		// intrusion detection evaluation.  See
		// http://www.ll.mit.edu/mission/communications/cyber/CSTcorpora/ideval/data/1999/training/week1/index.html
		// for more details.
		fmt.Println("Local pcap file", filename, "doesn't exist, reading from", *url)
		if resp, err := http.Get(*url); err != nil {
			panic(err)
		} else if out, err := os.Create(filename); err != nil {
			panic(err)
		} else if gz, err := gzip.NewReader(resp.Body); err != nil {
			panic(err)
		} else if n, err := io.Copy(out, gz); err != nil {
			panic(err)
		} else if err := gz.Close(); err != nil {
			panic(err)
		} else if err := out.Close(); err != nil {
			panic(err)
		} else {
			fmt.Println("Successfully read", n, "bytes from url, unzipped to local storage")
		}
	}
	fmt.Println("Reading file once through to hopefully cache most of it")
	if f, err := os.Open(filename); err != nil {
		panic(err)
	} else if n, err := io.Copy(ioutil.Discard, f); err != nil {
		panic(err)
	} else if err := f.Close(); err != nil {
		panic(err)
	} else {
		fmt.Println("Read in file", filename, ", total of", n, "bytes")
	}
	if *cpuProfile != "" {
		if cpu, err := os.Create(*cpuProfile); err != nil {
			panic(err)
		} else if err := pprof.StartCPUProfile(cpu); err != nil {
			panic(err)
		} else {
			defer func() {
				pprof.StopCPUProfile()
				cpu.Close()
			}()
		}
	}
	for i := 0; i < *repeat; i++ {
		fmt.Printf("%d/%d: Opening file %q for read\n", i+1, *repeat, filename)
		if h, err := pcap.OpenOffline(filename); err != nil {
			panic(err)
		} else {
			realStart := time.Now()
			// Read all packets into memory with BufferPacketSource.
			packetDataSource := NewBufferPacketSource(h)
			packetSource := gopacket.NewPacketSource(packetDataSource, h.LinkType())
			packetSource.DecodeOptions.Lazy = *decodeLazy
			packetSource.DecodeOptions.NoCopy = *decodeNoCopy
			count, errors := 0, 0
			start := time.Now()
			for packet, err := packetSource.NextPacket(); err != io.EOF; packet, err = packetSource.NextPacket() {
				if err != nil {
					fmt.Println("Error reading in packet:", err)
				}
				count++
				var hasError bool
				if *printErrors && packet.ErrorLayer() != nil {
					fmt.Println("\n\n\nError decoding packet:", packet.ErrorLayer().Error())
					fmt.Println(hex.Dump(packet.Data()))
					fmt.Printf("%#v\n", packet.Data())
					errors++
					hasError = true
				}
				if *printLayers || hasError {
					fmt.Printf("\n=== PACKET %d ===\n", count)
					for _, l := range packet.Layers() {
						fmt.Printf("--- LAYER %v ---\n%#v\n\n", l.LayerType(), l)
					}
					fmt.Println()
				}
			}
			duration := time.Since(start)
			fmt.Printf("Read in %v packets in %v, %v per packet\n", count, duration, duration/time.Duration(count))
			if *printErrors {
				fmt.Printf("%v errors, successfully decoded %.02f%%\n", errors, float64(count-errors)*100.0/float64(count))
			}
			fmt.Printf("Time to both read and process: %v\n", time.Since(realStart))
		}
	}
}
