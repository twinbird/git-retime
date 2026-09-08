# git-retime

現在のブランチのコミット日時をエディタで一括変更する、小さな Git サブコマンドです。

```sh
git retime 5
```

HEAD から first-parent で直近 5 件を、古い順に表示します。引数省略時は 10 件、履歴が短い場合は存在する分だけを表示します。

## インストール

macOS / Linux、Git 2.48 以降、ビルドには Go 1.23 以降が必要です。外部 Go モジュールは使用しません。

このディレクトリで実行します。

```sh
go build -o git-retime ./cmd/git-retime
```

生成した `git-retime` を PATH が通ったディレクトリへ配置してください。Go のインストール先が PATH に入っている場合は、次の方法も使えます。

```sh
go install ./cmd/git-retime
git retime -h
```

## 編集方法

```text
# AUTHOR_DATE              COMMITTER_DATE           HASH          SUBJECT
2026-09-08T18:32:10+09:00  2026-09-08T18:32:20+09:00  92ac42f133e1  fix: validation
2026-09-08T19:05:42+09:00  2026-09-08T19:06:01+09:00  f8527ed419ab  add endpoint
```

先頭 2 列の日時だけを変更し、保存してエディタを終了すると反映します。形式は `YYYY-MM-DDTHH:mm:ss±HH:mm` または `YYYY-MM-DDTHH:mm:ssZ` です。小数秒やタイムゾーン省略は受け付けません。同じ瞬間でもタイムゾーンのオフセットが変われば変更として扱います。

ハッシュ・件名・行順を変更したり、行を追加・削除するとエラーになります。ハッシュと件名の間の 2 スペースも維持してください。件名は元メッセージの先頭行で、制御文字などはエスケープして表示します。コメントと空行は追加できます。

入力エラーやエディタの異常終了では履歴を変更しません。再度コマンドを実行して編集してください。終了コードは正常・無変更・明示キャンセルが 0、エラーが 1 です。

## エディタとキャンセル

エディタは `git var GIT_EDITOR` で選択します。優先順は `GIT_EDITOR`、`core.editor`、`VISUAL`、`EDITOR`、Git の既定値です。[Git の仕様](https://git-scm.com/docs/git-var)

GUI エディタには終了を待つオプションが必要です。たとえば:

```sh
GIT_EDITOR='code --wait' git retime 5
```

変更せずに終了すれば何もしません。途中保存した内容はディスクに残るため、途中保存後に「保存せず閉じる」だけでは、それ以前に保存した変更をキャンセルできません。すべて取り消すには、次の行を追加して保存してください。

```text
# abort
```

空ファイルにして保存する方法や、エディタを異常終了させる方法（Vim の `:cq` など）でも反映を中止します。一時ファイルは処理終了時に削除します。

## 変更範囲と復旧

日時と、それに伴って変わる parent OID だけを変更します。コミットの tree、氏名・メール、メッセージ、その他のヘッダは保持します。作業ファイルと index は変更しないため、未コミット変更がある状態でも使えます。

コミットの日時が変わるとハッシュも変わり、選択範囲内の子孫も parent の変更によって再生成されます。マージコミットの parent の順序は維持します。first-parent 以外の side branch を再生成しないため、マージ経由で旧コミットが引き続き到達可能な場合があります。他のブランチ・タグ・notes は移動せず、push も行いません。

更新時は、旧 HEAD の一致を検証して Git の参照更新ロックを取得し、ロック中にブランチ名を確認してから確定します。旧 HEAD は `refs/retime/backups/<時刻>-<ID>` に保存し、ブランチ更新と同じトランザクションで作成します。[Git の参照更新](https://git-scm.com/docs/git-update-ref/2.48.0)

終了時に新旧 HEAD、バックアップ ref、次の形式の復旧コマンドを表示します。

```text
git update-ref 'refs/heads/main' <old-head> <new-head>
```

表示された実際の OID を含むコマンドを使ってください。その後ブランチが進んでいれば復旧は拒否されます。バックアップは自動削除しません。参照の更新前に中断した場合、途中生成した未参照オブジェクトは Git の通常の GC に任せます。

v1 では以下を受け付けません。

- 再生成対象に `gpgsig`、`gpgsig-sha256`、`mergetag` がある履歴。再生成不要な署名付き祖先はそのまま保持します。
- detached HEAD、コミットがないブランチ、shallow repository、replace refs、grafts、Git namespace。
- merge / rebase / cherry-pick / revert の処理中。
- Windows。

## 開発・検証

```sh
go test ./...
go vet ./...
go build ./cmd/git-retime
```

テストは一時リポジトリと代替エディタを使います。SHA-1 / SHA-256、マージ、linked worktree、メタデータの保持、未コミット変更の保持、競合時の中止と復旧を検証します。CI は macOS / Linux で実行します。

## GitHub Releases への公開

Release ワークフローは `v1.0.0` などのタグを push すると起動します。macOS / Linux のテストが成功した後、各 OS の amd64 / arm64 用バイナリをビルドし、README を含む 4 個の `.tar.gz` と SHA-256 の `checksums.txt` を GitHub Release に添付します。リリースノートは自動生成します。

変更をコミットして GitHub に push した後、公開するコミットにタグを付けます。

```sh
git tag v1.0.0
git push origin v1.0.0
```

`v1.0.0-rc.1` のようなタグはプレリリースとして公開します。認証には Actions の `GITHUB_TOKEN` を使うため、追加のシークレット登録は不要です。同じタグの公開済み Release は上書きしません。公開途中の失敗で Release が残った場合は、GitHub 上の状態を確認してから再実行してください。

ローカルで配布ファイルだけを作る場合は、空の出力ディレクトリを指定します。

```sh
bash scripts/release-build.sh v1.0.0 /tmp/git-retime-release
```

ダウンロードしたアーカイブを展開し、`git-retime` を PATH 上に配置してください。実行先には Git 2.48 以降が必要です。
