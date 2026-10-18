#!/bin/sh
# Disposable local Docker metadata + shared-Caddy data-path acceptance.
set -eu
cd "$(dirname "$0")/.."
run="funnel-path-qa-$$"
image="${FUNNEL_PATH_ECHO_IMAGE:-node:24.18.0-alpine}"
api=""
admin=""
cleanup() {
  [ -z "$api" ] || docker rm -f "$api" >/dev/null 2>&1 || true
  [ -z "$admin" ] || docker rm -f "$admin" >/dev/null 2>&1 || true
  docker network rm "$run" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM
docker network create "$run" >/dev/null
echo_js='const http=require("http"),crypto=require("crypto");
const s=http.createServer((r,w)=>{
 if(r.url==="/stream"){w.write("first\n");w.end("second\n");return;}
 let chunks=[];r.on("data",b=>chunks.push(b));r.on("end",()=>{w.setHeader("Content-Type","application/json");w.end(JSON.stringify({Backend:process.env.ECHO_NAME,URI:r.url,Host:r.headers.host,Method:r.method,Body:Buffer.concat(chunks).toString(),Client:r.headers["x-forwarded-for"]||"",Forwarded:r.headers.forwarded||"",Spoof:r.headers["x-forwarded-secret"]||""}));});
});
s.on("upgrade",(r,socket)=>{
 if(r.url!=="/ws"){socket.destroy();return;}
 const accept=crypto.createHash("sha1").update(r.headers["sec-websocket-key"]+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
 socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n");
 let pending=Buffer.alloc(0);socket.on("data",data=>{pending=Buffer.concat([pending,data]);while(pending.length>=6){let size=pending[1]&127;if(size>125){socket.destroy();return;}if(pending.length<6+size)return;const op=pending[0]&15,mask=pending.subarray(2,6),body=Buffer.from(pending.subarray(6,6+size));for(let i=0;i<body.length;i++)body[i]^=mask[i%4];pending=pending.subarray(6+size);socket.write(Buffer.concat([Buffer.from([128|op,size]),body]));if(op===8){socket.end();return;}}});
});s.listen(8080,"0.0.0.0");'
api=$(docker run --rm -d --name "$run-api" --network "$run" --read-only --cap-drop ALL --user 65534:65534 \
  -p 127.0.0.1::8080 -e ECHO_NAME=api \
  --label "funnel.path-qa=$run" \
  --label tailscale-private-funnel.routes.api.hostname=app.example.test \
  --label tailscale-private-funnel.routes.api.port=8080 \
  --label tailscale-private-funnel.routes.api.path-prefix=/api \
  --label tailscale-private-funnel.routes.api.replace-prefix=/ \
  "$image" node -e "$echo_js")
admin=$(docker run --rm -d --name "$run-admin" --network "$run" --read-only --cap-drop ALL --user 65534:65534 \
  -p 127.0.0.1::8080 -e ECHO_NAME=admin \
  --label "funnel.path-qa=$run" \
  --label tailscale-private-funnel.routes.admin.hostname=app.example.test \
  --label tailscale-private-funnel.routes.admin.port=8080 \
  --label tailscale-private-funnel.routes.admin.path-prefix=/api/admin \
  --label tailscale-private-funnel.routes.admin.replace-prefix=/internal \
  "$image" node -e "$echo_js")
api_address=$(docker port "$api" 8080/tcp)
admin_address=$(docker port "$admin" 8080/tcp)
for id in "$api" "$admin"; do
  docker exec "$id" node -e 'const http=require("http");let n=0;function check(){http.get("http://127.0.0.1:8080/",r=>{r.resume();process.exit(0)}).on("error",()=>{if(++n>30)process.exit(1);setTimeout(check,100)})}check()'
done
FUNNEL_PATH_DOCKER_FIXTURE="$run" go test ./internal/gateway -run TestLiveDockerPathDeclarations -count=1
FUNNEL_PATH_ECHO_API="$api_address" FUNNEL_PATH_ECHO_ADMIN="$admin_address" \
  go test -tags caddy ./internal/proxy -run TestPrefixRoutingAndRewriteThroughCaddy -count=1
