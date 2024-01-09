FROM	golang:1.21-alpine3.19
WORKDIR	/app
RUN	apk add --no-cache  --repository=https://dl-cdn.alpinelinux.org/alpine/v3.19/community fuse fuse3 git npm
RUN	git clone https://github.com/lemondevxyz/ft
RUN	git clone https://github.com/lemondevxyz/ft-demo
RUN	git clone https://github.com/lemondevxyz/ft-client
WORKDIR	/app/ft-client
RUN	npm install -d
RUN	npm run build
WORKDIR	/app
RUN	mkdir ft/client ft/static
RUN	cp -r ft-client/out/404.html ft/client/
RUN	cp -r "ft-client/out/[[...slug]].html" ft/client/index.html
RUN	cp -r ft-client/out/_next ft/static
WORKDIR	/app/ft
RUN	go build
WORKDIR	/app/ft-demo
RUN	go build
WORKDIR	/app
RUN	mkdir directories
WORKDIR	/app/directories
RUN	mkdir fuse container template
WORKDIR	/app/directories/template
RUN	mkdir -p dir/subdir
RUN	echo "asd" > somefile
RUN	echo "some other file" > dir/subfile
RUN	echo "other other other file" > dir/subdir/helo
WORKDIR	/app
ENV	FT_ADDR=unix!/app/ft.sock
ENV	FT_DEMO_ADDR=0.0.0.0:12345
WORKDIR	/app
RUN	mkdir /scripts
RUN	echo "cd /app/ft/; go run . /app/directories/fuse &" > /scripts/run.sh
RUN	echo "sleep 2; cd /app/ft-demo/; go run . /app/directories/container /app/directories/fuse /app/directories/template" >> /scripts/run.sh
CMD sh -c "sh /scripts/run.sh"
EXPOSE 12345