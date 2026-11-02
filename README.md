# air-sensor-api

ENS160（空気質）と AHT21（温度・湿度）を I2C で読み、HTTP の JSON で返す常駐サービス。Go 製。Linux（Raspberry Pi など）で動く。

- ライセンス：AGPL-3.0-only（[LICENSE](LICENSE)）
- リポジトリ：<https://github.com/furu04/air-sensor-api>

> **状態：実機で未検証（experimental）。** センサーとの通信はデータシートに合わせて書いてあるが、実機未検証です。1週間以内に検証予定。

## 注意

- **eCO2 は CO2 の実測ではない。** ENS160 が VOC と水素の検出から換算した推定値で、JSON のキーは `eco2_ppm`。換気の閾値判定に使わない。
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

## 設定（環境変数）

空文字列は未設定と同じ。

| 変数 | 既定 | 内容 |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:8080` | 待ち受け。全インターフェースなら `0.0.0.0:8080` か `:8080` を明示 |
| `I2C_BUS` | `1` | 0〜255 |
| `ENS160_ADDR` | `0x53` | `0x52` か `0x53` |
| `AHT21_ADDR` | `0x38` | `0x08`〜`0x77`。`ENS160_ADDR` と別の値 |
| `INTERVAL` | `60s` | 整数秒、5 秒以上 1 時間以下 |
| `TEMP_OFFSET` | `0` | ℃、−20〜20。`temp_c` と ENS160 への補正の両方に反映 |
| `HISTORY_SIZE` | `1440` | 1〜100000。履歴はメモリ上だけ |
| `STALE_AFTER` | `3 × INTERVAL` | `INTERVAL` 以上 |
| `NO_CRC` | `false` | AHT21 の CRC 検査を止める |
| `API_TOKEN` | 未設定 | 設定すると `/healthz` 以外で `Authorization: Bearer <token>` を要求。16 文字以上 |

## エンドポイント

`GET` と `HEAD` のみ。

| パス | 内容 |
|---|---|
| `/v1/readings/latest` | 最新の 1 件。無ければ `503`（`no_data`） |
| `/v1/readings` | 履歴。`window`、`since`、`limit`、`valid_only` |
| `/v1/status` | サンプラーの状態、バージョン、ソースの場所 |
| `/healthz` | `200` か `503`（`text/plain`）。認証不要 |

鮮度は単調時計の `age_seconds` で判定する。`ts` は RTC の無いボードで誤っていることがある。`valid` が `false` の間は `aqi`・`tvoc_ppb`・`eco2_ppm` を信用しない。

## ソースの提供（AGPL-3.0 §13）

`/v1/status` の `source` がソースの場所を返す。改変して公開する場合は、ビルド時に自分の版の URL へ変える。

```bash
go build -ldflags "-X main.sourceURL=https://example.com/your/fork" .
```
