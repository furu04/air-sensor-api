# air-sensor-api

ENS160（空気質）、AHT21（温度・湿度）、SCD40 / SCD41（CO2 実測）を I2C で読み、HTTP の JSON で返す常駐サービス。Go 製。3 つのセンサーは個別に有効・無効を選べる。Linux（Raspberry Pi など）で動く。

- ライセンス：AGPL-3.0-only（[LICENSE](LICENSE)）
- リポジトリ：<https://github.com/furu04/air-sensor-api>

> **状態：実機で未検証（experimental）。** センサーとの通信はデータシートに合わせて書いてあるが、実機未検証です。1週間以内に検証予定。

## 注意

- **eCO2 は CO2 の実測ではない。** ENS160 が VOC と水素の検出から換算した推定値で、JSON のキーは `eco2_ppm`。換気の閾値判定に使わない。CO2 の実測値が要るなら SCD40 / SCD41 を有効にして `co2_ppm` を使う。
- **安全用途・生命に関わる用途に使わない。** ENS160 のデータシートが明記している。
- 認証の無い読み取り専用 API。待ち受けの既定はループバック（`127.0.0.1:8080`）。外へ出すなら `API_TOKEN` と、TLS を終端するリバースプロキシ（nginx、Caddy など）を使う。TLS はこのサービスでは実装しない。

## ビルドと配置

```bash
git clone https://github.com/furu04/air-sensor-api
cd air-sensor-api
make build
sudo install -m 0755 air-sensor-api /usr/local/bin/
sudo useradd --system --no-create-home --shell /usr/sbin/nologin air-sensor
sudo install -m 0644 air-sensor-api.service /etc/systemd/system/
sudo install -d -m 0755 /etc/air-sensor-api
sudo install -m 0600 /dev/null /etc/air-sensor-api/env
sudo systemctl daemon-reload && sudo systemctl enable --now air-sensor-api
```

`make build` は arm64 向け。32bit OS なら `GOARCH=arm GOARM=7` で自分でビルドする。I2C を有効にし（`raspi-config`）、サービスのユーザーが `i2c` グループに入ること。`i2c` グループが無い環境（コンテナなど）では、`systemctl edit air-sensor-api` で `User=root` に上書きするか、デバイスのグループに合わせる。`PrivateDevices=true` は付けない（ユニットは `DevicePolicy=closed` と `DeviceAllow=char-i2c rw` で I2C だけを許可している）。

`/etc/air-sensor-api/env` に `KEY=value` 形式で設定を書く（例：`API_TOKEN=...`）。トークンを `Environment=` に直接書かない（`systemctl show` で読める）。

I2C バスを同時に使えるのは 1 プロセスだけ。他に I2C を使うプロセスと同時に動かさない。

### 旧版からの更新

旧版は、AHT21 と ENS160 だけを読み、環境変数だけで設定する版。設定を変えなければ、新版の応答は旧版と同一（`SCD4X_ENABLE` が既定で無効のため）。履歴はメモリ上だけなので、再起動で消える。

```bash
sudo cp /usr/local/bin/air-sensor-api /usr/local/bin/air-sensor-api.old
sudo cp /etc/systemd/system/air-sensor-api.service /root/air-sensor-api.service.old
make build
sudo systemctl stop air-sensor-api
sudo install -m 0755 air-sensor-api /usr/local/bin/
sudo install -m 0644 air-sensor-api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start air-sensor-api
journalctl -u air-sensor-api -n 20 --no-pager
curl -s http://127.0.0.1:8080/v1/status
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/healthz
```

- 戻すときは、`.old` の 2 つを元の場所へ戻し、`daemon-reload` して再起動する。
- 新しいユニットは、`Environment=LISTEN_ADDR` / `INTERVAL` / `TEMP_OFFSET` を持たない（環境変数は設定ファイルより優先されるため、残すと設定ファイルの同名の値が効かない）。値は既定と同じなので、設定ファイルを使わないかぎり動作は変わらない。旧ユニットを残す場合は、`/etc/air-sensor-api/env` にこれらを書いて上書きする。
- `/etc/air-sensor-api/env` はそのまま使える。
- 設定ファイルは、サービスのユーザー（`air-sensor`）が読める必要がある。秘密情報は入れないので、`sudo install -m 0644 -o root -g root config.json /etc/air-sensor-api/config.json` でよい。`env` が 0600 なのは、systemd が root で読むため。
- SCD4x を足すときは、配線の後で `i2cdetect -y 1` に `62` が出ることを確かめ、`SCD4X_ENABLE=true`（または `config.json`）を足して再起動する。

## 設定

優先順位は、環境変数 ＞ 設定ファイル（任意）＞ 既定値。空文字列は未設定と同じ。

### 有効にするセンサーを選ぶ

| センサー | 既定 | 環境変数 | 設定ファイルのキー |
|---|---|---|---|
| AHT21（温湿度） | 有効 | `AHT21_ENABLE` | `sensors.aht21.enabled` |
| ENS160（AQI・TVOC・eCO2） | 有効 | `ENS160_ENABLE` | `sensors.ens160.enabled` |
| SCD40 / SCD41（CO2） | **無効** | `SCD4X_ENABLE` | `sensors.scd4x.enabled` |

少なくとも 1 つは有効にする（0 個だと起動を拒否する）。無効なセンサーには I2C アクセスを一切しない。有効なセンサーが 1 つでも読めないと、そのサンプルは保存されない。センサーを 1 つずつ足して動作を確かめたいときは、まず 1 つだけ有効にする。

- AHT21 を無効にすると、温湿度は SCD4x が有効ならその値で出る（精度は AHT21 より落ちる。次節）。SCD4x も無効なら `temp_c`・`rh_pct` が出なくなり、ENS160 への温湿度補正も止まる（ENS160 の既定の補正である 25 ℃・50 %RH で動くので、精度は落ちる）。
- ENS160 を無効にすると、`aqi`・`tvoc_ppb`・`eco2_ppm`・`validity`・`validity_name` が出なくなり、`valid` は常に `true` になる。
- SCD4x を有効にすると `co2_ppm` が出る（後述）。

### 設定ファイル（JSON）

`CONFIG_FILE` にパスを指定したときだけ読む。systemd なら `/etc/air-sensor-api/env` に `CONFIG_FILE=/etc/air-sensor-api/config.json` と書く。

```json
{
  "interval": "60s",
  "temp_offset": -1.5,
  "sensors": {
    "aht21":  { "enabled": true, "addr": "0x38" },
    "ens160": { "enabled": true, "addr": "0x53" },
    "scd4x":  { "enabled": true }
  }
}
```

- 省略したキーは既定値（または環境変数）になる。
- 使えるキー：`listen_addr`、`i2c_bus`、`interval`、`temp_offset`、`history_size`、`stale_after`、`no_crc`、`sensors.{aht21,ens160}.{enabled,addr}`、`sensors.scd4x.enabled`。それぞれ同名の環境変数（大文字、`sensors.` 配下は上の表のとおり）と同じ意味と制約を持つ。
- 未知のキー、型の違い、JSON の後ろの余分な内容、64 KiB を超えるファイルはエラーで、起動しない。
- **`API_TOKEN` は設定ファイルに書けない**（書くと未知のキーとして拒否される）。0600 の環境変数ファイルに置く。
- `addr` は `"0x38"` のような文字列でも、10 進の数値でもよい。SCD4x のアドレスは `0x62` 固定で、`addr` キーは無い。
- JSON はコメントを書けない。説明を残したいときは、設定ファイルを置くディレクトリに別の README を置く。

### 環境変数

空文字列は未設定と同じ。

| 変数 | 既定 | 内容 |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:8080` | 待ち受け。全インターフェースなら `0.0.0.0:8080` か `:8080` を明示 |
| `I2C_BUS` | `1` | 0〜255 |
| `ENS160_ADDR` | `0x53` | `0x52` か `0x53` |
| `AHT21_ADDR` | `0x38` | `0x08`〜`0x77`。有効なセンサー同士で重複しないこと（`ENS160_ADDR` と別。SCD4x が有効なら `0x62` 以外） |
| `AHT21_ENABLE` / `ENS160_ENABLE` / `SCD4X_ENABLE` | `true` / `true` / `false` | センサーの有効化。`true`・`1` など |
| `INTERVAL` | `60s` | 整数秒、5 秒以上 1 時間以下 |
| `TEMP_OFFSET` | `0` | ℃、−20〜20。`temp_c` と ENS160 への補正の両方に反映 |
| `HISTORY_SIZE` | `1440` | 1〜100000。履歴はメモリ上だけ |
| `STALE_AFTER` | `3 × INTERVAL` | `INTERVAL` 以上 |
| `NO_CRC` | `false` | AHT21 の CRC 検査を止める。SCD4x の CRC 検査は止められない |
| `CONFIG_FILE` | 未設定 | JSON 設定ファイルのパス。環境変数でのみ指定できる |
| `API_TOKEN` | 未設定 | 設定すると `/healthz` 以外で `Authorization: Bearer <token>` を要求。16 文字以上 |

## エンドポイント

`GET` と `HEAD` のみ。

| パス | 内容 |
|---|---|
| `/v1/readings/latest` | 最新の 1 件。無ければ `503`（`no_data`） |
| `/v1/readings` | 履歴。`window`、`since`、`limit`、`valid_only` |
| `/v1/status` | サンプラーの状態、バージョン、ソースの場所、有効なセンサーのアドレス |
| `/healthz` | `200` か `503`（`text/plain`）。認証不要 |

## SCD40 / SCD41（任意）

`SCD4X_ENABLE=true`（または設定ファイルの `sensors.scd4x.enabled`）で有効になる。有効にすると `/v1/readings/latest` と `/v1/readings` の各要素に `co2_ppm`（整数、ppm）が加わり、`/v1/status` に `scd4x` が加わる。無効のときは、これらのキーは出ない。

- 温湿度は、AHT21 が有効なら常に AHT21 の値を使い、SCD4x の温湿度は捨てる。AHT21 が無効のときだけ、SCD4x の温湿度を `temp_c`・`rh_pct` として出し、ENS160 が有効ならその補正にも使う。
- SCD4x の温度は、周期測定中の自己発熱を含み、AHT21 より精度が落ちる。SCD4x 内部の温度オフセットは書き込まない。実測の温度計と比べ、`TEMP_OFFSET`（℃、負の値で下げる）で補正する。`TEMP_OFFSET` は温度にだけ効き、湿度は再計算しない。温度を数℃補正した場合、湿度は同程度に狂うと見なす。
- 温湿度の精度を求めるなら、AHT21 を SCD4x から離して付ける。
- SCD40 と SCD41 は区別しない。周期測定（5 秒周期）だけを使う。
- 起動直後の最初のサンプルは、最初の測定が終わるまで最大で約 6 秒待つ。
- SCD4x の読み出しに失敗すると、そのサンプル全体を保存しない（ENS160・AHT21 の値も捨てる）。5 回連続で失敗すると `/healthz` が `503` になり、バスを開き直す。
- `valid` は ENS160 のウォームアップ状態だけを表す（ENS160 が無効なら常に `true`）。`co2_ppm` の信頼性は表さない。
- 自動自己校正（ASC）はセンサーの工場既定（有効）のまま。ASC は、週に一度は屋外の空気（約 400 ppm）に触れる使い方を前提にしている。常に換気されない密閉空間では、値が低い方へずれる。高度補正・気圧補正は設定しない。

応答のキーは、有効なセンサーのものだけが出る（無効なセンサーのキーは `null` ではなく、キー自体が無い）。

鮮度は単調時計の `age_seconds` で判定する。`ts` は RTC の無いボードで誤っていることがある。`valid` が `false` の間は `aqi`・`tvoc_ppb`・`eco2_ppm` を信用しない。

## ソースの提供（AGPL-3.0 §13）

`/v1/status` の `source` がソースの場所を返す。改変して公開する場合は、ビルド時に自分の版の URL へ変える。

```bash
go build -ldflags "-X main.sourceURL=https://example.com/your/fork" .
```
