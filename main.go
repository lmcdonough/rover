package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

//go:embed ui/dist
var frontend embed.FS

func main() {
	log.Println("Starting Rover...")

	var tfPath, workingDir, name string
	flag.StringVar(&tfPath, "tfPath", "/usr/local/bin/terraform", "Path to Terraform binary")
	flag.StringVar(&workingDir, "workingDir", ".", "Path to Terraform configuration")
	flag.StringVar(&name, "name", "rover", "Configuration name")
	flag.Parse()

	// Generate assets
	plan, rso, mapDM, graph := generateAssets(name, workingDir, tfPath)

	// Save to file (debug)
	// saveJSONToFile(name, "plan", "output", plan)
	// saveJSONToFile(name, "rso", "output", rso)
	// saveJSONToFile(name, "map", "output", mapDM)
	// saveJSONToFile(name, "graph", "output", graph)

	// Embed frontend
	stripped, err := fs.Sub(frontend, "ui/dist")
	if err != nil {
		log.Fatalln(err)
	}
	frontendFS := http.FileServer(http.FS(stripped))

	http.Handle("/", frontendFS)
	http.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fileType := strings.Replace(r.URL.Path, "/api/", "", 1)

		var j []byte
		var err error

		enableCors(&w)

		switch fileType {
		case "plan":
			j, err = json.Marshal(plan)
			if err != nil {
				io.WriteString(w, fmt.Sprintf("Error producing JSON: %s\n", err))
			}
		case "rso":
			j, err = json.Marshal(rso)
			if err != nil {
				io.WriteString(w, fmt.Sprintf("Error producing JSON: %s\n", err))
			}
		case "map":
			j, err = json.Marshal(mapDM)
			if err != nil {
				io.WriteString(w, fmt.Sprintf("Error producing JSON: %s\n", err))
			}
		case "graph":
			j, err = json.Marshal(graph)
			if err != nil {
				io.WriteString(w, fmt.Sprintf("Error producing JSON: %s\n", err))
			}
		default:
			io.WriteString(w, "Please enter a valid file type: plan, rso, map, graph\n")
		}

		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, bytes.NewReader(j))
	})

	log.Println("Done generating assets.")
	log.Println("Rover is running on localhost:9000")

	err = http.ListenAndServe(":9000", nil)
	if err != nil {
		log.Fatalf("Could not start server: %s\n", err.Error())
	}

}

func generateAssets(name string, workingDir string, tfPath string) (*tfjson.Plan, *ResourcesOverview, *Map, Graph) {
	// Generate Plan
	plan, err := generatePlan(name, workingDir, tfPath)
	if err != nil {
		log.Printf(fmt.Sprintf("Unable to parse Plan: %s", err))
		os.Exit(2)
	}

	// Parse Configuration
	log.Println("Parsing configuration...")
	// Get current directory file
	config, _ := tfconfig.LoadModule(workingDir)
	if config.Diagnostics.HasErrors() {
		os.Exit(1)
	}

	// Generate RSO
	log.Println("Generating resource overview...")
	rso := GenerateResourceOverview(plan)

	// Generate Map
	log.Println("Generating resource map...")
	mapDM := GenerateMap(config, rso)

	// Generate Graph
	log.Println("Generating resource graph...")
	graph := GenerateGraph(plan, mapDM)

	return plan, rso, mapDM, graph
}

func generatePlan(name string, workingDir string, tfPath string) (*tfjson.Plan, error) {
	tmpDir, err := ioutil.TempDir("", "rover")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	tf, err := tfexec.NewTerraform(workingDir, tfPath)
	if err != nil {
		return nil, err
	}

	log.Println("Initializing Terraform...")
	// err = tf.Init(context.Background(), tfexec.Upgrade(true), tfexec.LockTimeout("60s"))
	err = tf.Init(context.Background(), tfexec.Upgrade(true))
	if err != nil {
		return nil, err
	}

	log.Println("Generating plan...")
	planPath := fmt.Sprintf("%s/%s-%v", tmpDir, "roverplan", time.Now().Unix())
	_, err = tf.Plan(context.Background(), tfexec.Out(planPath))
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Unable to run Plan: %s", err))
	}

	plan, err := tf.ShowPlanFile(context.Background(), planPath)

	return plan, err
}

func showJSON(g interface{}) {
	j, err := json.Marshal(g)
	if err != nil {
		log.Printf("Error producing JSON: %s\n", err)
		os.Exit(2)
	}
	log.Printf("%+v", string(j))
}

func showModuleJSON(module *tfconfig.Module) {
	j, err := json.MarshalIndent(module, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error producing JSON: %s\n", err)
		os.Exit(2)
	}
	os.Stdout.Write(j)
	os.Stdout.Write([]byte{'\n'})
}

func saveJSONToFile(prefix string, fileType string, path string, j interface{}) string {
	b, err := json.Marshal(j)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error producing JSON: %s\n", err)
		os.Exit(2)
	}

	newpath := filepath.Join(".", fmt.Sprintf("%s/%s", path, prefix))
	err = os.MkdirAll(newpath, os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(fmt.Sprintf("%s/%s-%s.json", newpath, prefix, fileType))

	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	_, err2 := f.WriteString(string(b))

	if err2 != nil {
		log.Fatal(err2)
	}

	// log.Printf("Saved to %s", fmt.Sprintf("%s/%s-%s.json", newpath, prefix, fileType))

	return fmt.Sprintf("%s/%s-%s.json", newpath, prefix, fileType)
}

func enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}