# ft-demo
ft-demo is a wrapper around [`ft`](https://github.com/lemondevxyz/ft) that enables random users to try ft. checkout the graph below to understand how `ft-demo` works.

## can I run it?
Yeah, just clone the repository, cd into it and execute the following commands:
```sh
docker build -t ft-demo - < Dockerfile
docker run --rm -d --device /dev/fuse --privileged -p 127.0.0.1:12345:12345 ft-demo
```

Then, go to [localhost:12345](http://localhost:12345) and voilà.

## sweet, can I try?
yeah, just go to [ft.lemondev.xyz](https://ft.lemondev.xyz).

note: this instance will restart everyday at 00:00 MYT Time.

## okay, cool, but how can I run it?
ahh, just compile the main.go file. Afterwards, create an init script for ft-demo, here's [mine](ft-demo.openrc) that works on Alpine linux in OpenRC.

After that is done, simply reverse-proxy `ft-demo` and you should be good to go.

### what if I use systemd?
Well, you have to create your own script file. Do not fret, however, it is pretty painless. For `ft-demo`, make sure of the following:
1. `ft` is running
2. `ft` is run in a directory where `client` and `static` exist
3. `ft-demo` can connect to ft's unix or tcp socket
4. before `ft-demo` launches, the fuse directory is unmounted
5. check for any pesky CORS headers