package main

import (
	"crypto/sha1"
	"fmt"
	"strings"
	"os"
	"log"
	//"bytes"
	"os/exec"
	"time"
)

func gitPull() (string, []byte) {
	exec.Command("git","reset","--hard","origin/master").Run()
	cmd := exec.Command("git","pull")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	if gr := exec.Command("grep", "-q", "user-jyfgzcx0", "LEDGER.txt").Run(); gr != nil {
		f, err := os.OpenFile("LEDGER.txt", os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		if _, err = f.WriteString("user-jyfgzcx0: 1\n"); err != nil {
			panic(err)
		}
		f.Close()
	}

	exec.Command("git", "add", "LEDGER.txt").Run()

	tree, _ := exec.Command("git", "write-tree").Output() 
	parent, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	timestamp := time.Now().UTC().Unix()
	difficulty, _:= exec.Command("cat", "difficulty.txt").Output()

	return fmt.Sprintf(`tree %sparent %sauthor CTF user <me@example.com> %d +0000
committer CTF user <me@example.com> %d +0000

Get my Gitcoin`, tree, parent, timestamp, timestamp), []byte(strings.TrimSpace(fmt.Sprintf("%s",difficulty)))
}

func main() {
	header, difficulty := gitPull()
	start := time.Now().UTC().Unix()
	
	fmt.Printf("\n\ndifficulty='%s'\n", difficulty)

	for counter := 1; counter < 25000000; counter += 1 {	
		msg := fmt.Sprintf("%s\n\nz%d\n", header, counter)
		body := fmt.Sprintf("commit %d\x00%s", len(msg), msg)
		commitSha1 := sha1.Sum([]byte(body))
		commitId := fmt.Sprintf("%x", commitSha1)
		if counter % 262144 == 0 {
		 	fmt.Printf(".")
		}
		if strings.HasPrefix(commitId, "000000"){//bytes.Compare(commitSha1[:], difficulty) < 0 {
			fmt.Printf("Mined a Gitcoin with commit: %s, counter=%d\n", commitId, counter)
			fmt.Printf("Done in: %d\n", time.Now().UTC().Unix() - start)
			cmd := exec.Command("git", "hash-object", "-w", "-t", "commit", "--stdin")
			cmd.Stdin = strings.NewReader(msg)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Print("running git hash-object")
			cmd.Run()

			exec.Command("git", "reset", "--hard", commitId).Run()
			cmd = exec.Command("git", "push", "origin", "master")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run() 
			if err != nil {	
				log.Print(err)
				log.Print("restarting")
				start = time.Now().UTC().Unix()
				header, difficulty = gitPull()
				counter = 1
			} else {
				fmt.Printf("Hurray!!!")
				return
			}
		}

	}
	fmt.Printf("end loop")

}
// ecdbaea94aea5014831bfd567c6e6bdb59247f08
