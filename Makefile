k6: *.go go.sum
	xk6 build master --with github.com/jeffutter/xk6-mutlipart-http=. --replace github.com/jeffutter/xk6-mutlipart-http/events=./events
compile-docker:
	docker run --rm -it -e GOOS=darwin -u "$(id -u):$(id -g)" -v "${PWD}:/xk6" \
	grafana/xk6 build master \
	--with xk6-multipartHTTP=.
compile-docker-linux:
	docker run --rm -it -u "$(id -u):$(id -g)" -v "${PWD}:/xk6" \
	grafana/xk6 build master \
	--with xk6-multipartHTTP=.
run: k6
	./k6 run $(ARGS) --vus 1 --duration 200s Examples/test.js
