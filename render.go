package main

import (
	"io"
	"os"
	"fmt"
	"sort"
	"math"
	"time"
	"bytes"
	"image"
	"runtime"
	"image/png"
	"errhandler"
	"image/draw"
	"image/color"
	"encoding/gob"
	"compress/zlib"
	"path/filepath"
	"encoding/binary"
	"github.com/bemasher/GoNBT"
)

const (
	DIR = `C:\Users\bemasher\Desktop\Other\Minecraft\world`
	// DIR = `small`
	GLOBPATTERN = "region/*.mca"
	IMGFILE = "iso.png"
	BLOCKCOLORSFILE = "blocks.gob"
	
	DIM = 1024
	NCPUS = 4
)

var (
	big binary.ByteOrder
	blockColors map[byte]BlockColor
)

type Reverse struct {
	sort.Interface
}

func (r Reverse) Less(i, j int) bool {
	return r.Interface.Less(j, i)
}

type Region struct {
	X, Z int
	Path string
}

func NewRegion(file string) Region {
	var r Region
	r.Path = file
	fmt.Sscanf(filepath.Base(file), "r.%d.%d.mca", &r.X, &r.Z)
	return r
}

func (r Region) Bounds() image.Rectangle {
	xr0, zr0 := r.X << 9, r.Z << 9
	xr1, zr1 := (r.X + 1) << 9, (r.Z + 1) << 9
	x0, y0 := xr0 << 1 + zr0 << 1, -xr0 + zr1
	x1, y1 := xr1 << 1 + zr1 << 1, -xr1 - 512 + zr0
	return image.Rect(x0 - 2, y0 + 2, x1 - 2, y1)
}

type RegionList []Region

func (rl RegionList) Len() int {
	return len(rl)
}

func (rl RegionList) Less(i, j int) bool {
	if rl[i].Z == rl[j].Z {
		return rl[j].X < rl[i].X
	}
	return rl[i].Z < rl[j].Z
}

func (rl RegionList) Swap(i, j int) {
	rl[i], rl[j] = rl[j], rl[i]
}

type Header struct {
	Locations [DIM]Location
	Timestamps [DIM]int32
}

func (h *Header) Read(r io.Reader) {
	for i := 0; i < DIM; i++ {
		h.Locations[i].Read(r)
	}
	
	for i := 0; i < DIM; i++ {
		binary.Read(r, big, &h.Timestamps[i])
	}
}

type Location struct {
	Offset uint32
	Length byte
}

func (rl *Location) Read(r io.Reader) {
	binary.Read(r, big, &rl.Offset)
	rl.Length = uint8(rl.Offset & 0x000000FF)
	rl.Offset >>= 8
}

type Level struct {
	X int32 `nbt:"xPos"`
	Z int32 `nbt:"zPos"`
	LastUpdate int64
	TerrainPopulated byte
	HeightMap []int32
	Sections []Section
}

type ChunkList []Level

func (rl ChunkList) Len() int {
	return len(rl)
}

func (rl ChunkList) Less(i, j int) bool {
	if rl[i].Z == rl[j].Z {
		return rl[j].X < rl[i].X
	}
	return rl[i].Z < rl[j].Z
}

func (rl ChunkList) Swap(i, j int) {
	rl[i], rl[j] = rl[j], rl[i]
}

type Section struct {
	Y byte
	Data []byte
	Blocks []byte
}

func (s Section) String() string {
	return fmt.Sprintf("{Y: %d Data: %d... Blocks: %d...}", s.Y, s.Data[:Min(6, len(s.Data))], s.Blocks[:Min(6, len(s.Blocks))])
}

func (s Section) Block(x, y, z int) byte {
	return s.Blocks[(y * 16 + z) * 16 + x]
}

func (c Level) String() string {
	return fmt.Sprintf("{X: %d Z: %d LastUpdate: %d TerrainPopulated: %d HeightMap: %d...}",
		c.X, c.Z,
		c.LastUpdate,
		c.TerrainPopulated,
		c.HeightMap[:Min(6, len(c.HeightMap))],
		// c.Sections[:Min(6, len(c.Sections))],
	)
}

func Min(a ...int) (min int) {
	min = math.MaxInt32
	for _, i := range a {
		if i < min {
			min = i
		}
	}
	return
}

func (l *Level) Read(r io.Reader) {
	var (
		length int32
		compression byte
	)
	
	binary.Read(r, big, &length)
	binary.Read(r, big, &compression)
	
	rawLevelData, err := zlib.NewReader(r)
	errhandler.Handle("Error zlib decompressing chunk data: ", err)
	
	levelData := bytes.NewBuffer(nil)
	defer levelData.Reset()
	
	levelData.ReadFrom(rawLevelData)
	rawLevelData.Close()
	
	defer func() {
		recover()
	}()
	
	nbt.Read(levelData, l)
}

func (l *Level) Bounds() image.Rectangle {
	y := int32(0)
	
	for _, section := range l.Sections {
		if y < int32(section.Y) << 4 {
			y = int32(section.Y) << 4
		}
	}
	
	y += 16
	
	x0, y0 := int(l.X << 5 + l.Z << 5), int(-(l.X << 4) + (l.Z + 1) << 4)
	x1, y1 := int((l.X + 1) << 5 + (l.Z + 1) << 5), int(-(l.X + 1) << 4 - y << 1 + l.Z << 4)
	return image.Rect(x0 - 2, y0 + 2, x1 - 2, y1)
}

func ProjectIsometric(x, y, z int) (xI, yI int) {
	xI = x << 1 + z << 1
	yI = -x - y << 1 + z
	return
}

type BlockColor struct {
	Alpha byte
	Full bool
	Top, Left, Right color.RGBA
}

func DrawBlock(img *image.RGBA, x, y int, c BlockColor) {
	var blockImg *image.RGBA
	if c.Alpha == 0xFF {
		blockImg = img.SubImage(image.Rect(x - 2, y, x + 2, y + 3)).(*image.RGBA)
	} else {
		blockImg = image.NewRGBA(image.Rect(x - 2, y, x + 2, y + 3))
	}
	
	if c.Full {
		blockImg.SetRGBA(x, y, c.Top)
		blockImg.SetRGBA(x + 1, y, c.Top)
		blockImg.SetRGBA(x - 2, y, c.Top)
		blockImg.SetRGBA(x - 1, y, c.Top)
		
		blockImg.SetRGBA(x - 2, y + 1, c.Left)
		blockImg.SetRGBA(x - 1, y + 1, c.Left)
		blockImg.SetRGBA(x - 1, y + 2, c.Left)
		blockImg.SetRGBA(x - 2, y + 2, c.Left)
		
		blockImg.SetRGBA(x, y + 1, c.Right)
		blockImg.SetRGBA(x, y + 2, c.Right)
		blockImg.SetRGBA(x + 1, y + 1, c.Right)
		blockImg.SetRGBA(x + 1, y + 2, c.Right)
	} else {
		blockImg.SetRGBA(x, y + 1, c.Top)
		blockImg.SetRGBA(x + 1, y + 1, c.Top)
		blockImg.SetRGBA(x - 2, y + 1, c.Top)
		blockImg.SetRGBA(x - 1, y + 1, c.Top)
		
		blockImg.SetRGBA(x - 2, y + 2, c.Left)
		blockImg.SetRGBA(x - 1, y + 2, c.Left)
		
		blockImg.SetRGBA(x, y + 2, c.Right)
		blockImg.SetRGBA(x + 1, y + 2, c.Right)
	}
	
	if c.Alpha != 0xFF {
		draw.DrawMask(img, blockImg.Bounds(), blockImg, image.Pt(x - 2, y), image.NewUniform(color.RGBA{c.Alpha, c.Alpha, c.Alpha, c.Alpha}), image.Pt(x - 2, y), draw.Over)
	}
}

func (l Level) Draw(img *image.RGBA) {
	for _, section := range l.Sections {
		for y := 0; y < 16; y++ {
			for x := 15; x >= 0; x-- {
				for z := 0; z < 16; z++ {
					if blockColor, exists := blockColors[section.Block(x, y, z)]; exists {
						xISO, yISO := ProjectIsometric(int(l.X) << 4 + x, int(section.Y) << 4 + y, int(l.Z) << 4 + z)
						DrawBlock(img, xISO, yISO, blockColor)
					}
				}
			}
		}
	}
}

type Job struct {
	Filename string
	ChunkCount int
	Chunks ChunkList
}

func Alloc() uint64 {
	m := new(runtime.MemStats)
	runtime.ReadMemStats(m)
	return m.Alloc
}

func init() {
	big = binary.BigEndian
	
	blockColorsFile, err := os.Open(BLOCKCOLORSFILE)
	errhandler.Handle("Error opening block color file: ", err)
	defer blockColorsFile.Close()
	
	blockDecoder := gob.NewDecoder(blockColorsFile)
	blockDecoder.Decode(&blockColors)
}

func main() {
	defer func() {
		recover()
	}()
	start := time.Now()
	
	files, err := filepath.Glob(filepath.Join(DIR, GLOBPATTERN))
	errhandler.Handle("Error globbing region files: ", err)
	
	var (
		regions RegionList
		imgBounds image.Rectangle
		chunkBounds image.Rectangle
	)
	
	for _, file := range files {
		region := NewRegion(file)
		
		if imgBounds == image.Rect(0, 0, 0, 0) {
			imgBounds = region.Bounds()
		} else {
			imgBounds = imgBounds.Union(region.Bounds())
		}
		
		regions = append(regions, region)
	}
	
	fmt.Printf("Max image dimensions: %+v\n", imgBounds.Size())
	img := image.NewRGBA(imgBounds)
	
	sort.Sort(regions)
	work := make(chan Job)
	
	go func(work chan Job) {
		for _, region := range regions {
			regionFile, err := os.Open(region.Path)
			errhandler.Handle("Error opening region file: ", err)
			
			var header Header
			header.Read(regionFile)
			
			var chunks ChunkList
			for _, location := range header.Locations {
				if location.Length != 0 {
					chunkSection := io.NewSectionReader(regionFile, int64(location.Offset) << 12, int64(location.Length) << 12)
					
					var chunk Level
					chunk.Read(chunkSection)
					if chunk.TerrainPopulated == 1 {
						chunks = append(chunks, chunk)
					}
				}
			}
			
			
			regionFile.Close()
			
			sort.Sort(chunks)
			work <- Job{filepath.Base(region.Path), len(chunks), chunks}
		}
		close(work)
	}(work)
	
	for job := range work {
		fmt.Printf("Parsing: %s\n", job.Filename)
		fmt.Printf("\tFound %d populated chunks\n", job.ChunkCount)
		for i, chunk := range job.Chunks {
			if chunkBounds == image.Rect(0, 0, 0, 0) {
				chunkBounds = chunk.Bounds()
			} else {
				chunkBounds = chunkBounds.Union(chunk.Bounds())
			}
			
			fmt.Printf("\tRendering: %0.1f%% (%d/%d)\r", 100.0 * float64(i + 1) / float64(job.ChunkCount), i + 1, job.ChunkCount)
			chunk.Draw(img)
		}
		fmt.Println()
	}
	
	stop := time.Since(start)
	fmt.Printf("Render time: %+v\n", stop)
	
	fmt.Printf("Rendered image dimensions: %+v\n", chunkBounds.Size())
	
	imgFile, err := os.Create(IMGFILE)
	errhandler.Handle("Error creating image file: ", err)
	defer imgFile.Close()
	
	fmt.Println("Committing image to disk...")
	png.Encode(imgFile, img.SubImage(chunkBounds))
}
