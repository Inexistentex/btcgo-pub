package search

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"sync/atomic"
	"time"

	"btcgo/wif"

	"github.com/dustin/go-humanize"
)

const progressFile = "progress.dat"

type Range struct {
	Min    string `json:"min"`
	Max    string `json:"max"`
	Status string `json:"status"`
}

type Ranges struct {
	Ranges []Range
}

type Interval struct {
	Min *big.Int
	Max *big.Int
}

type BlockData struct {
	Min    [64]byte `json:"min"`
	Max    [64]byte `json:"max"`
	Status int64    `json:"status"`
}

type IntervalNode struct {
	Interval Interval
	Max      *big.Int
	Left     *IntervalNode
	Right    *IntervalNode
}

type IntervalTree struct {
	Root *IntervalNode
}

func (tree *IntervalTree) Insert(interval Interval) {
	tree.Root = insert(tree.Root, interval)
}

func insert(node *IntervalNode, interval Interval) *IntervalNode {
	if node == nil {
		return &IntervalNode{
			Interval: interval,
			Max:      new(big.Int).Set(interval.Max),
		}
	}

	if interval.Min.Cmp(node.Interval.Min) < 0 {
		node.Left = insert(node.Left, interval)
	} else {
		node.Right = insert(node.Right, interval)
	}

	if node.Max.Cmp(interval.Max) < 0 {
		node.Max = new(big.Int).Set(interval.Max)
	}

	return node
}

func (tree *IntervalTree) Overlaps(min, max *big.Int) bool {
	return overlaps(tree.Root, min, max)
}

func overlaps(node *IntervalNode, min, max *big.Int) bool {
	if node == nil {
		return false
	}

	if min.Cmp(node.Interval.Max) <= 0 && max.Cmp(node.Interval.Min) >= 0 {
		return true
	}

	if node.Left != nil && min.Cmp(node.Left.Max) <= 0 {
		return overlaps(node.Left, min, max)
	}

	return overlaps(node.Right, min, max)
}

var (
	blockCounter   int64
	assignedBlocks = make(map[int]Interval)
	blockBuffer    []BlockData // Buffer para armazenar os blocos
	bufferCounter  int64       // Contador para rastrear a quantidade de blocos no buffer
)

func SearchInBlocks(wallets []string, blocksRead *int64, blockSize int64, minPrivKey, maxPrivKey *big.Int, stopSignal chan struct{}, startTime time.Time, intervalTree *IntervalTree, keysChecked *int64, id int) {
	for {
		select {
		case <-stopSignal:
			return
		default:
			block, actualBlockSize := getRandomBlockWithVariableSize(minPrivKey, maxPrivKey, blockSize, intervalTree)

			blockNumber := atomic.AddInt64(&blockCounter, 1)
			assignedBlocks[id] = Interval{Min: block, Max: new(big.Int).Add(block, big.NewInt(actualBlockSize-1))}

			searchInBlock(wallets, block, actualBlockSize, stopSignal, keysChecked, startTime)

			delete(assignedBlocks, id)

			saveProgress(atomic.LoadInt64(blocksRead), block, new(big.Int).Add(block, big.NewInt(actualBlockSize-1)), int(blockNumber))
			atomic.AddInt64(blocksRead, 1)
		}
	}
}

func getRandomBlockWithVariableSize(minPrivKey, maxPrivKey *big.Int, initialBlockSize int64, intervalTree *IntervalTree) (*big.Int, int64) {
	blockSize := initialBlockSize

	for blockSize > 0 {
		block := new(big.Int).Rand(rand.New(rand.NewSource(time.Now().UnixNano())), new(big.Int).Sub(maxPrivKey, minPrivKey))
		block.Add(block, minPrivKey)

		blockEnd := new(big.Int).Add(block, big.NewInt(blockSize-1))

		// Ajustar o tamanho do bloco para não ultrapassar maxPrivKey
		if blockEnd.Cmp(maxPrivKey) > 0 {
			blockSize = new(big.Int).Sub(maxPrivKey, block).Int64() + 1
			blockEnd = new(big.Int).Add(block, big.NewInt(blockSize-1))
		}

		// Verificar se o bloco está dentro dos limites e não sobrepõe intervalos existentes
		if block.Cmp(minPrivKey) >= 0 && blockEnd.Cmp(maxPrivKey) <= 0 && !intervalTree.Overlaps(block, blockEnd) {
			intervalTree.Insert(Interval{Min: block, Max: blockEnd})
			return block, initialBlockSize // Voltar ao tamanho inicial do bloco
		}

		// Reduzir o tamanho do bloco de forma dinâmica baseado na quantidade de sobreposição
		overlapDetected := false
		if intervalTree.Overlaps(block, blockEnd) {
			overlapDetected = true
		}

		// Ajustar dinamicamente o tamanho do bloco
		if overlapDetected {
			blockSize = int64(float64(blockSize) * 0.9) // Reduzir o tamanho do bloco em 10%
		} else {
			blockSize = initialBlockSize // Redefinir para o tamanho inicial
		}
	}

	// Retornar um bloco mínimo caso não seja possível encontrar um bloco adequado
	return minPrivKey, 1
}

func searchInBlock(targetPublicKeys []string, block *big.Int, actualBlockSize int64, stopSignal chan struct{}, keysChecked *int64, startTime time.Time) {
	for i := int64(0); i < actualBlockSize; i++ {
		select {
		case <-stopSignal:
			return
		default:
			privKey := new(big.Int).Add(block, big.NewInt(i))
			pubKey := wif.GeneratePublicKey(privKey.Bytes())
			pubKeyHex := fmt.Sprintf("%x", pubKey)

			if contains(targetPublicKeys, pubKeyHex) {
				wifKey := wif.PrivateKeyToWIF(privKey)
				saveFoundKeyDetails(privKey, wifKey, pubKeyHex)
				close(stopSignal)
				return
			}

			atomic.AddInt64(keysChecked, 1)
			if atomic.LoadInt64(keysChecked)%actualBlockSize == 0 {
				elapsedTime := time.Since(startTime).Seconds()
				rate := float64(atomic.LoadInt64(keysChecked)) / elapsedTime
				fmt.Printf("Chaves verificadas: %s, Taxa: %.2f chaves/s\n", humanize.Comma(atomic.LoadInt64(keysChecked)), rate)
			}
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func saveFoundKeyDetails(privKey *big.Int, wifKey, pubKeyHex string) {
	fmt.Println("-------------------CHAVE ENCONTRADA!!!!-------------------")
	fmt.Printf("Private key: %064x\n", privKey)
	fmt.Printf("WIF: %s\n", wifKey)
	fmt.Printf("Chave Pública: %s\n", pubKeyHex)

	file, err := os.OpenFile("found_keys.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Erro ao salvar chave encontrada: %v\n", err)
		return
	}
	defer file.Close()

	_, err = file.WriteString(fmt.Sprintf("Private key: %064x\nWIF: %s\nChave Pública: %s\n", privKey, wifKey, pubKeyHex))
	if err != nil {
		fmt.Printf("Erro ao escrever chave encontrada: %v\n", err)
	}
}

func saveProgress(blocksRead int64, min, max *big.Int, blockNumber int) {
	var blockData BlockData
	copy(blockData.Min[:], min.Bytes())
	copy(blockData.Max[:], max.Bytes())
	blockData.Status = blocksRead

	blockBuffer = append(blockBuffer, blockData)
	atomic.AddInt64(&bufferCounter, 1)

	if atomic.LoadInt64(&bufferCounter) >= 1 {
		file, err := os.OpenFile(progressFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Printf("Erro ao salvar progresso: %v\n", err)
			return
		}
		defer file.Close()

		for _, data := range blockBuffer {
			err = binary.Write(file, binary.LittleEndian, data)
			if err != nil {
				fmt.Printf("Erro ao escrever progresso: %v\n", err)
				return
			}
		}

		blockBuffer = blockBuffer[:0] // Limpar o buffer
		atomic.StoreInt64(&bufferCounter, 0) // Resetar o contador
	}
}

func LoadRanges(filename string) (*Ranges, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var ranges Ranges
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&ranges)
	if err != nil {
		return nil, err
	}

	return &ranges, nil
}
