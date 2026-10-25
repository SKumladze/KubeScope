.PHONY: all web build run dev cluster-up cluster-down cluster-down-all collector logs clean-history demo clean

all: build

web:
	cd web && npm install --silent && npm run build
	touch collector/internal/server/dist/.keep

build: web
	cd collector && go build -o ../bin/kubescope .

# Start KubeScope (Docker container) and the kind cluster, open http://localhost:8090
cluster-up:
	./cluster/up.sh

# Remove the cluster and the KubeScope container. History in ./data is kept.
cluster-down:
	./cluster/down.sh

# Remove EVERYTHING: cluster, KubeScope, history, images. Shows the list and asks first.
# DRY_RUN=1 only shows the list; YES=1 skips the question.
cluster-down-all:
	./cluster/down-all.sh

# Rebuild and restart only the KubeScope container (after changing its code).
collector:
	docker build -q -t kubescope:local . >/dev/null
	docker image prune -f --filter label=io.kubescope.image=collector >/dev/null
	docker rm -f kubescope-collector >/dev/null 2>&1 || true
	docker run -d --name kubescope-collector --network kind --user $$(id -u):$$(id -g) \
		-v $(CURDIR)/data:/data -v /etc/localtime:/etc/localtime:ro -p 127.0.0.1:8090:8090 --restart unless-stopped kubescope:local $(ARGS) >/dev/null
	@echo "KubeScope restarted: http://localhost:8090"

logs:
	docker logs -f kubescope-collector

# Delete all recorded history.
clean-history:
	rm -f data/kubescope.db data/kubescope.db-wal data/kubescope.db-shm

# For working on KubeScope itself: run it as a process (cluster created with MODE=host).
# For the UI with hot reload, also run `cd web && npm run dev` and open http://localhost:5173
run: build
	./bin/kubescope -db data/kubescope.db

dev:
	cd collector && go run . -db ../data/kubescope.db

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
