#!/bin/sh
docker run --rm -v ~/taro/data:/data alpine sh -c 'apk add -q sqlite && sqlite3 /data/taro.db "SELECT substr(r.title,1,60), r.resolution, r.seeders FROM resources r JOIN entries e ON r.id=e.selected_resource_id WHERE e.title='"'"'Shingeki no Kyojin'"'"' LIMIT 2;"'
