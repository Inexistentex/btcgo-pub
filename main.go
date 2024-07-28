package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"btcgo/search"

	"github.com/fatih/color"
)

const (
	progressFile = "progress.dat"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	ranges, err := search.LoadRanges("ranges.json")
	if err != nil {
		log.Fatalf("Failed to load ranges: %v", err)
	}

	color.Cyan("BTCGO - Investidor Internacional")
	color.White("v0.1")
	color.Cyan("BTCGO - Mod BY: Inex")
	color.White("v0.7")

	// Exibe as wallets a serem buscadas
	rangeNumber := getRandomRange(len(ranges.Ranges), make(map[int]bool))
	privKeyHex := ranges.Ranges[rangeNumber-1].Min
	maxPrivKeyHex := ranges.Ranges[rangeNumber-1].Max
	wallets := strings.Split(ranges.Ranges[rangeNumber-1].Status, ", ")

	privKeyInt := new(big.Int)
	privKeyInt.SetString(privKeyHex[2:], 16)
	maxPrivKeyInt := new(big.Int)
	maxPrivKeyInt.SetString(maxPrivKeyHex[2:], 16)

	fmt.Println("Wallets a serem buscadas:")
	for _, wallet := range wallets {
		fmt.Println(wallet)
	}

	// Exibe o número de intervalos carregados
	fmt.Printf("%d intervalos carregados\n", len(ranges.Ranges))

	jumpInterval := promptJumpInterval()
	numGoroutines := promptNumGoroutines()
	blockSize := promptBlockSize()

	startOption := promptStartOption()

	var intervalTree search.IntervalTree
	var blocksRead int64
	if startOption == 1 {
		resetProgress()
		blocksRead = 0
	} else {
		readIntervals, br := loadProgress(blockSize)
		blocksRead = br
		for _, interval := range readIntervals {
			intervalTree.Insert(interval)
		}
		fmt.Printf("Carregados %d intervalos. Pressione Enter para continuar...", len(readIntervals))
		bufio.NewReader(os.Stdin).ReadBytes('\n')
	}

	keysChecked := blocksRead * blockSize
	startTime := time.Now()

	clearScreen()

	stopSignal := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			search.SearchInBlocks(wallets, &blocksRead, blockSize, privKeyInt, maxPrivKeyInt, stopSignal, startTime, &intervalTree, &keysChecked, id)
		}(i)
	}

	go func() {
		ticker := time.NewTicker(time.Duration(jumpInterval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rangeNumber = getRandomRange(len(ranges.Ranges), make(map[int]bool))
				privKeyHex = ranges.Ranges[rangeNumber-1].Min
				maxPrivKeyHex = ranges.Ranges[rangeNumber-1].Max
				privKeyInt.SetString(privKeyHex[2:], 16)
				maxPrivKeyInt.SetString(maxPrivKeyHex[2:], 16)
				wallets = strings.Split(ranges.Ranges[rangeNumber-1].Status, ", ")
				fmt.Println("Saltando para o próximo intervalo.")
			case <-stopSignal:
				return
			}
		}
	}()

	wg.Wait()
}

func promptJumpInterval() int {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("Digite o intervalo de tempo em segundos para saltar para um novo intervalo: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		jumpInterval, err := strconv.Atoi(input)
		if err == nil && jumpInterval > 0 {
			return jumpInterval
		}
		fmt.Println("Intervalo de tempo inválido, tente novamente.")
	}
}

func getRandomRange(numRanges int, usedRanges map[int]bool) int {
	for {
		rangeNumber := rand.Intn(numRanges) + 1
		if !usedRanges[rangeNumber] {
			usedRanges[rangeNumber] = true
			if len(usedRanges) == numRanges {
				usedRanges = make(map[int]bool) // Resetar após usar todos os intervalos
			}
			return rangeNumber
		}
	}
}

func promptNumGoroutines() int {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("Digite o número de threads: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		numGoroutines, err := strconv.Atoi(input)
		if err == nil && numGoroutines > 0 {
			return numGoroutines
		}
		fmt.Println("Número de threads inválido, tente novamente.")
	}
}

func promptBlockSize() int64 {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("Digite o tamanho do bloco (ex: 100000): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		blockSize, err := strconv.ParseInt(input, 10, 64)
		if err == nil && blockSize > 0 {
			return blockSize
		}
		fmt.Println("Tamanho do bloco inválido, tente novamente.")
	}
}

func promptStartOption() int {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("1. Iniciar nova busca")
		fmt.Println("2. Continuar busca anterior")
		fmt.Print("Escolha uma opção: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		option, err := strconv.Atoi(input)
		if err == nil && (option == 1 || option == 2) {
			return option
		}
		fmt.Println("Opção inválida, tente novamente.")
	}
}

func resetProgress() {
	os.Remove(progressFile)
}

func loadProgress(blockSize int64) ([]search.Interval, int64) {
	file, err := os.Open(progressFile)
	if err != nil {
		return nil, 0
	}
	defer file.Close()

	var intervals []search.Interval
	var lastBlockNumber int64

	for {
		var blockData search.BlockData
		err := binary.Read(file, binary.LittleEndian, &blockData)
		if err != nil {
			break
		}
		minInt := new(big.Int)
		minInt.SetString(string(blockData.Min[:]), 16)
		maxInt := new(big.Int)
		maxInt.SetString(string(blockData.Max[:]), 16)
		lastBlockNumber = blockData.Status

		intervals = append(intervals, search.Interval{Min: minInt, Max: maxInt})
	}

	return intervals, lastBlockNumber
}

func clearScreen() {
	cmd := exec.Command("clear")
	cmd.Stdout = os.Stdout
	cmd.Run()
}