package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type (
	FileId        uint16
	FileId2Path   map[FileId]string
	Matches       []FileId
	Ngram2Matches map[string]Matches

	Index struct {
		Ngram2Matches Ngram2Matches
		FileId2Path   FileId2Path
		NextFileId    FileId
		BasePath      string
		Completed     bool
	}

	ResponseRoot struct {
		Success bool     `json:"success"`
		Results []string `json:"results,omitempty"`
	}
)

func (index *Index) WasBuilt() bool {
	return index.Completed
}

func (index *Index) WalkTree(root string) {
	index.BasePath = root
	index.Completed = false // harness calls us only once.
	visit := func(path string, f os.FileInfo, err error) error {
		if err == nil && !f.IsDir() {
			fileId, wasIndexedAlready := index.AppendFile(path)
			if wasIndexedAlready == true {
				return nil
			}
			index.IndexFile(path, fileId)
		}
		return nil
	}
	filepath.Walk(root, visit)
	index.Completed = true
}

func (index *Index) IndexFile(fullPath string, fileId FileId) {
	content, _ := ioutil.ReadFile(fullPath)
	index.IndexContent(string(content), fileId)
}

func (index *Index) AppendFile(fullpath string) (fileId FileId, wasIndexedAlready bool) {
	for fid, indexedFilename := range index.FileId2Path {
		if indexedFilename == fullpath {
			return fid, true
		}
	}
	fileId = index.NextFileId
	index.FileId2Path[fileId] = fullpath
	index.NextFileId += 1
	return fileId, false
}

func (index *Index) Ngrams(content string) (set map[string]struct{}) {
	set = make(map[string]struct{})
	for _, word := range strings.Fields(content) {
		i := 0
		for {
			start := i
			_, width := utf8.DecodeRuneInString(word[i:])
			ngramEnd := start + width
			ngram := string(word[start:ngramEnd])
			//set[ngram] = struct{}{}
			i = i + width
			if i >= len(word) {
				break
			}

			_, width = utf8.DecodeRuneInString(word[i:])
			ngramEnd += width
			ngram = string(word[start:ngramEnd])
			//set[ngram] = struct{}{}
			i2 := i + width
			if i2 >= len(word) {
				continue
			}

			_, width = utf8.DecodeRuneInString(word[i:])
			ngramEnd += width
			ngram = string(word[start:ngramEnd])
			//set[ngram] = struct{}{}
			i3 := i2 + width
			if i3 >= len(word) {
				continue
			}

			_, width = utf8.DecodeRuneInString(word[i:])
			ngramEnd += width
			ngram = string(word[start:ngramEnd])
			set[ngram] = struct{}{}
			i4 := i3 + width
			if i4 >= len(word) {
				continue
			}

			_, width = utf8.DecodeRuneInString(word[i:])
			ngramEnd += width
			ngram = string(word[start:ngramEnd])
			//set[ngram] = struct{}{}
			i5 := i4 + width
			if i5 >= len(word) {
				continue
			}

			_, width = utf8.DecodeRuneInString(word[i5:])
			ngramEnd += width
			ngram = string(word[start:ngramEnd])
			set[ngram] = struct{}{}
		}
	}
	return set
}

func (index *Index) IndexContent(content string, fileId FileId) {
	ngrams := index.Ngrams(content)
	//fmt.Printf("Extracted %d trigraps in fileId=%d\n", len(trigrapsInFile), fileId)
	for ngram, _ := range ngrams {
		matches, present := index.Ngram2Matches[ngram]
		if present == false {
			matches = make(Matches, 0, 1)
		}
		matches = append(matches, fileId)
		index.Ngram2Matches[ngram] = matches
	}
}

func (index *Index) MatchingFileIds(word string) (commonSet map[FileId]struct{}) {
	commonSet = make(map[FileId]struct{})
	for fileId, _ := range index.FileId2Path {
		commonSet[fileId] = struct{}{}
	}
	for ngram, _ := range index.Ngrams(word) {
		matches := index.Ngram2Matches[ngram]
		// Perform intersect between commonSet and the rest
		set := make(map[FileId]struct{})
		for _, fileId := range matches {
			set[fileId] = struct{}{}
		}
		for fileId, _ := range commonSet {
			_, present := set[fileId]
			if present == false {
				delete(commonSet, fileId)
				if len(commonSet) == 0 {
					return commonSet
				}
			}
		}
	}
	return commonSet
}

func (index *Index) Query(word string) (results []string) {
	fileIdSet := index.MatchingFileIds(word)
	if len(fileIdSet) == 0 {
		return results
	}
	//fmt.Printf("'%s' eliminated %d files\n", word, len(index.FileId2Path) - len(fileIdSet))

	results = make([]string, 0, len(fileIdSet))

	for fileId, _ := range fileIdSet {
		fullPath := index.FileId2Path[fileId]
		relPath, _ := filepath.Rel(index.BasePath, fullPath)
		for _, hit := range index.MatchingLineNumbersInFile(fullPath, word) {
			results = append(results, fmt.Sprintf("%s:%d", relPath, hit))
		}
	}

	return results
}

func (index *Index) MatchingLineNumbersInFile(fullPath, word string) (hits []int) {
	hits = make([]int, 0, 1)
	lineSep := []byte("\n")
	querry := []byte(word)
	_, firstRuneWidth := utf8.DecodeRune(querry)
	content, _ := ioutil.ReadFile(fullPath)

	pos, lineCount := 0, 1
	for {
		pos = bytes.Index(content, querry)
		if pos == -1 {
			break
		}

		lineCount += bytes.Count(content[:pos], lineSep)
		hits = append(hits, lineCount)

		// advance to the next line
		content = content[(pos + firstRuneWidth):]
		firstNewLinePos := bytes.Index(content, lineSep)
		if firstNewLinePos == -1 {
			break
		}
		content = content[(firstNewLinePos + firstRuneWidth):]
		lineCount += 1
	}
	return hits
}

func httpReplyWith(res *ResponseRoot, w http.ResponseWriter) {
	json, _ := json.Marshal(res)
	bytesWritten, _ := fmt.Fprintf(w, "%s", json)
	// fmt.Printf("%s\n", json)
	w.Header().Set("Content-Length", strconv.Itoa(bytesWritten))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
}

var index *Index

func httpHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	startTime := time.Now()
	defer func() {
		elapsedTime := time.Now().Sub(startTime)
		fmt.Printf("Completed %s?%s in %0.1fms\n",
			path, r.URL.RawQuery, elapsedTime.Seconds()*1000.0)
	}()

	r.ParseForm()
	res := &ResponseRoot{Success: true}
	switch path {
	case "/healthcheck":
	case "/index":
		index.WalkTree(r.Form.Get("path"))
	case "/isIndexed":
		res.Success = index.WasBuilt()
	case "/":
		// assuming that we are called after the index is built here
		res.Results = index.Query(r.Form.Get("q"))
	}

	httpReplyWith(res, w)
}

func main() {
	index = &Index{
		Ngram2Matches: make(Ngram2Matches),
		FileId2Path:   make(FileId2Path),
	}
	fmt.Printf("Listening for connections ... \n")
	http.HandleFunc("/", httpHandler)
	http.ListenAndServe(":9090", nil)
}
