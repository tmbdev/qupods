# A Small Tool for Running Many Jobs

Big data and deep learning often involves running thousands of jobs,
for data preparation, ETL, inference, and hyperparameter search.
The `qupods` command is a small command line tool for scheduling and
running such collections of jobs on Kubernetes.

All you need is Kubernetes `Pod` specification and a list of items
to iterate it over.

Assume we want to process 5000 shards that are stored in a Google Cloud
Bucket with names like `gs://bucket/shard-002345.tar`.

The processing itself is done by a Kubernetes `Pod` containing
a processing script; this is parameterized by the job number and the
shard specification:


```YAML
apiVersion: v1
kind: Pod
metadata:
  name: "preprocess-{{.Index}}"
  labels:
    app: ubuntu-app
spec:
  containers:
  - name: process-shard
    image: preprocess-image
    command:
      - /bin/bash
      - -c
      - |
        gsutil cat gs://bucket/shard-{{.Item}}.tar |
        process-shard |
        gsutil cp - gs://bucket/output-{{.Item}}.tar |
    imagePullPolicy: IfNotPresent
    resources:
      requests:
        cpu: 2
        memory: 4G
      limits:
        cpu: 2
        memory: 4G
  restartPolicy: Never
```

Let's generate a list of items representing all the shards:

```Bash
$ seq -f %06.0f 5000 > shardlist
```

You can now schedule and run those 5000 jobs on whatever Kubernetes
cluster you happen to use with this command:

```Bash
$ qupods -i shardlist process.yaml
```

This will submit jobs for all the items in the `shardlist`, but it will
schedule them so that the Kubernetes scheduler only ever has a small number
of new jobs pending. When a job is finished, `qujobs` will automatically
retrieve the corresponding log file and store it in `./QUJOBS/name.log` or
`./QUJOBS/name.err`, depending on whether the job completed successfully
or had errors.

That's really all there is to it. There are a few options you can use:

- instead of a list of lines in a textfile, you can specify a JSON list with `-j`
- you can control the maximum number of pending and running jobs
- you can override the default `kubectl` command
- by default, `qupods` won't reschedule jobs for which there is log file already,
  but you can override that (or simply remove the log files)
- you can override the log directory (default `./QUJOBS`)

# Q&A

*Why don't you use the Kubernetes `Job` spec?*

It tends not to scale too well to thousands of jobs, and it doesn't
automatically download the logs. Furthermore, you still need a tool
to generate the sequence of job specifications from a template and items.

*Why don't you use a workflow system?*

Workflow systems are a good solution for many large scale processing
applications. But in return for being more powerful, they also tend to be
more difficult to configure. Which solution is better depends on your
application. For many simple problems, `qujobs -i items pod.yaml` is
an easier solution.

*I don't want to run this on my workstation because I may not remain connected
to the Kubernetes cluster. I want a job controller running on the cluster.*

You can run `qupods` inside its own Docker container on the cluster.
That is, `qupods` can function like many of the other Kubernetes workflow
and job controller systems that themselves run inside a Kubernetes cluster,
but instead of a special web interface and configuration language, you simply
execute a container with containing `qupods` and your scheduling problem described
in shell.
