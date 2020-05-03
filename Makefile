qupods: $(cmds) $(dpipes)
	go clean
	go build -o qupods qupods.go
	qupods -h

install: qupods
	cp qupods /usr/local/bin

test:
	microk8s.kubectl get nodes
	cd tests && ./run-tests
