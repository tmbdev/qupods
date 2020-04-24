package main

import (
	"fmt"
	"log"
	"io"
	"os"
	"strings"
	"bytes"
	"text/template"
	"os/exec"
	"errors"
	"path"
	"io/ioutil"
    "path/filepath"
	"encoding/json"
	"bufio"
	"time"

	"github.com/jessevdk/go-flags"
	"gopkg.in/yaml.v2"
)

var opts struct {
	Verbose bool   `short:"v"`
	PrintSpecs bool `short:"P"`
	NoFileStatus bool `short:"N"`
	Kubectl string `long:"kubectl" default:"microk8s kubectl"`
	Logdir string `long:"logdir" default:"./QUPODS"`
	NoRerun bool `long:"norerun"`
	NoWait bool `long:"nowait"`
	DryRun bool `long:"dryrun"`
	Poll float32 `long:"poll" default:"3.0"`
	Pace float32 `long:"pace" default:"1.0"`
	MaxRunning int `long:"maxrunning" default:"100000"`
	MaxPending int `long:"maxpending" default:"3"`
	ItemFile string `short:"i" long:"items"`
	JsonFile string `short:"j" long:"json"`
	Positional struct {
		Input string `required:"yes"`
	} `positional-args:"yes"`
}

var Parser = flags.NewParser(&opts, flags.Default)

func GetEnv(key, dflt string) string {
	value, present := os.LookupEnv(key)
	if present {
		return value
	}
	return dflt
}

func OpenLog(name, dflt string) *log.Logger {
	fname := GetEnv("log_"+name, dflt)
	stream, err := os.Create(fname)
	Handle(err)
	return log.New(stream, "["+name+"] ", 0)
}

var AllPhases []string = strings.Split(
	"None Pending Running Terminating Succeeded Failed"," ")
var infolog *log.Logger = OpenLog("info", "/dev/stderr")
var errlog *log.Logger = OpenLog("error", "/dev/stderr")
var debuglog *log.Logger = OpenLog("debug", "/dev/null")
var raw_status string = ""
var pod_status map[string]string = map[string]string{}
var status_counter map[string]int = map[string]int{}
var yamltemplate string = ""

// Handle errors.
func Handle(err error) {
	if err != nil {
		panic(err)
	}
}

// Validate arguments
func Validate(ok bool, args ...interface{}) {
	if ok {
		return
	}
	result := make([]string, len(args))
	for i, v := range args {
		result[i] = fmt.Sprintf("%v", v)
	}
	message := strings.Join(result, " ")
	fmt.Println("Error:", message)
	os.Exit(1)
}

func Sleep(t float32) {
	nanos := time.Duration(t * 1e9)
	time.Sleep(nanos)
}

type PodDescription struct {
	Metadata struct {
		Name string
	}
}

func GetPodName(data []byte) string {
	desc := PodDescription{}
	err := yaml.Unmarshal([]byte(data), &desc)
	Handle(err)
	return string(desc.Metadata.Name)
}

type TemplateVars struct {
	Index int
	Item string
	Values map[string]string
}

func ExpandVars(s string, vars TemplateVars) string {
	tmpl, err := template.New("").Parse(s)
	Handle(err)
	var buffer bytes.Buffer
	err = tmpl.Execute(&buffer, vars)
	Handle(err)
	return string(buffer.Bytes())
}

func KubeCtl(input string, args... string) ([]byte, error) {
	argv := strings.Split(opts.Kubectl, " ")
	argv = append(argv, args...)
	debuglog.Println(strings.Join(argv,"|"))
	proc := exec.Command(argv[0], argv[1:]...)
	if input != "" {
		stdin, err := proc.StdinPipe()
		Handle(err)
		go func() {
			defer stdin.Close()
			io.WriteString(stdin, input)
		}()
	}
	stderr, err := proc.StderrPipe()
	Handle(err)
	go func() {
		output, _ := ioutil.ReadAll(stderr)
		if string(output) != "" {
			errlog.Print(string(output))
		}
	}()
	out, err := proc.Output()
	return out, err
}

func ChangeStatus(podname, ostatus, nstatus string) {
	if nstatus == "Succeeded" || nstatus == "Failed" {
		if opts.Logdir == "" {
			return
		}
		logname := ""
		if nstatus == "Succeeded" {
			logname = path.Join(opts.Logdir, podname+".log")
		} else {
			logname = path.Join(opts.Logdir, podname+".err")
		}
		data, err := KubeCtl("", "logs", "pod/"+podname)
		Handle(err)
		ioutil.WriteFile(logname, data, 0666)
		_, err = KubeCtl("", "delete", "pod/"+podname)	
		Handle(err)
	}
}

func GetFileStatus() {
	if opts.NoFileStatus {
		return
	}
	if opts.Logdir == "" {
		return
	}
	logs, err := filepath.Glob(path.Join(opts.Logdir, "*.log"))
	Handle(err)
	for _, f := range logs {
		f = path.Base(f)
		f = strings.TrimSuffix(f, path.Ext(f))
		pod_status[f] = "Succeeded"
		debuglog.Println("logstatus", f, "Succeeded")
	}
	errs, err := filepath.Glob(path.Join(opts.Logdir, "*.err"))
	Handle(err)
	for _, f := range errs {
		f = path.Base(f)
		f = strings.TrimSuffix(f, path.Ext(f))
		pod_status[f] = "Failed"
		debuglog.Println("logstatus", f, "Failed")
	}
}

type PodStatus struct {
	Items []struct {
		Metadata struct {
			Name string
		}
		Status struct {
			Phase string
		}
	}
}

func KuPoll() {
	pod_status = map[string]string{}
	GetFileStatus()
	status := PodStatus{}
	data, err := KubeCtl("", "get", "pods", "-o", "json")
	Handle(err)
	json.Unmarshal(data, &status)
	for _, k := range AllPhases {
		status_counter[k] = 0
	}
	for _, item := range status.Items {
		podname := item.Metadata.Name
		status := item.Status.Phase
		ostatus := pod_status[podname]
		pod_status[podname] = status
		if ostatus != status {
			ChangeStatus(podname, ostatus, status)
		}
		status_counter[status]++
	}
}

func ReadItems(fname string) []map[string]string {
	items, err := os.Open(fname)
	Handle(err)
	defer items.Close()
	scanner := bufio.NewScanner(items)
	result := make([]map[string]string, 0, 10)
	for scanner.Scan() {
		item := map[string]string{"item": scanner.Text()}
		result = append(result, item)
	}
	return result
}

func ReadItemsJson(fname string) []map[string]string {
	data, err := ioutil.ReadFile(fname)
	Handle(err)
	var result []map[string]string
	err = json.Unmarshal(data, &result)
	return result
}

func CountActive() int {
	active := status_counter["Pending"]
	active += status_counter["Running"]
	active += status_counter["Terminating"]
	return active
}

func GetStatus() string {
	return fmt.Sprintf("Pending %-3d Running %-6d Succeeded %-6d Failed %-6d",
				   status_counter["Pending"],
				   status_counter["Running"],
				   status_counter["Succeeded"],
				   status_counter["Failed"])
}

func main() {
	if len(os.Args) == 1 {
		Parser.WriteHelp(os.Stderr)
		os.Exit(1)
	}
	_, err := Parser.Parse()
	if err != nil {
		flagsErr, ok := err.(*flags.Error)
		if ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		} else {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	lstat, err := os.Stat(opts.Logdir)
	if err != nil {
		err := os.Mkdir(opts.Logdir, 0777)
		Handle(err)
		lstat, err = os.Stat(opts.Logdir)
		Handle(err)
	}
	Validate(lstat.IsDir(), "not a directory:", opts.Logdir)
	s, err := ioutil.ReadFile(opts.Positional.Input)
	Handle(err)
	yamltemplate = string(s)
	Validate(opts.ItemFile!= "", "must provide --itemfile")
	var items []map[string]string
	if opts.ItemFile != "" {
		Validate(opts.JsonFile == "", "must specify only one of itemfile, jsonfile")
		items = ReadItems(opts.ItemFile)
	} else if opts.JsonFile != "" {
		items = ReadItemsJson(opts.JsonFile)
	} else {
		panic(errors.New("must specify either itemfile or jsonfile"))
	}
	for index, dict := range items {
		vars := TemplateVars{index, dict["item"], dict}
		yaml := ExpandVars(yamltemplate, vars)
		podname := GetPodName([]byte(yaml))
		KuPoll()
		frac := fmt.Sprintf("%6d/%-6d", index, len(items))
		infolog.Println(frac, GetStatus())
		status := pod_status[podname]
		if status == "Succeeded" {
			continue
		}
		if opts.NoRerun && status == "Failed" {
			continue
		}
		for {
			pending := status_counter["Pending"]
		    running := status_counter["Running"]
			if pending <= opts.MaxPending && running+pending <= opts.MaxRunning {
				   break
			}
			Sleep(opts.Poll)
			KuPoll()
		}
		if opts.PrintSpecs {
			infolog.Println(yaml)
		}
		KubeCtl(yaml, "apply", "-f", "-")
		Sleep(opts.Pace)
	}
	if !opts.NoWait {
		for {
			if CountActive() == 0 {
				break
			}
			Sleep(opts.Poll)
			KuPoll()
			infolog.Println("waiting", GetStatus())
		}
	}
	KuPoll()
}
