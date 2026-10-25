.PHONY: all web build run dev cluster-up cluster-down demo clean

all: build

web:
	cd web && npm install --silent && npm run build
	touch collector/internal/server/dist/.keep

build: web
	cd collector && go build -o ../bin/kubescope .

run: build
	./bin/kubescope

# Backend only, for working on the UI with hot reload: run `make dev` and
# `cd web && npm run dev`, then open http://localhost:5173
dev:
	cd collector && go run .

cluster-up:
	./cluster/up.sh

cluster-down:
	./cluster/down.sh

# Something to watch: creates, scales and deletes a deployment.
demo:
	kubectl create deployment nginx --image=nginx
	sleep 8
	kubectl scale deployment nginx --replicas=3
	sleep 8
	kubectl delete deployment nginx

clean:
	rm -rf bin web/dist
	find collector/internal/server/dist -mindepth 1 ! -name .keep -delete
