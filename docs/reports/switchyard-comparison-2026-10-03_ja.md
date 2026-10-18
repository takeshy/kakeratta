# Switchyard比較試験 2026-10-03

> 状態（2026-10-03）: この比較結果を受け、Switchyard連携・比較コマンド・サンプル設定を撤去し、回答モデルの選択を従来のSuper/Ultra方式に戻しました。本レポートは検証記録として残し、試験構成のリンクは実験時のコミットを参照します。

[English](./switchyard-comparison-2026-10-03.md)

付属の合成5ケースを各方式1回実行。Token Factoryを両方式で使用。OpenRouterキーは未使用。回答生成と従来の熟考判定を測定し、ツール・検索・記憶・本番書き込みは実行しない。

## 結論

この試験ではSwitchyardを採用せず、従来のSuper/Ultra方式へ戻した。合成5ケースではSwitchyardが全ケースで遅く、熟考が必要と想定した `deeper-reply` でもLightningを選択した。

- 平均応答時間：従来方式2.84秒、Switchyard 9.56秒。
- 回答完了：10/10。ただし完了は回答内容の正しさを意味しない。
- 限界：各ケース1回のみ。検索・出典確認なし。USD費用は未算定。

読む順序: [結果](#結果) → [回答レビューと限界](#回答レビューと限界) → [試験構成](#試験構成) → [回答全文](#回答全文)。

## 結果

| ケース | 従来の回答モデル | 秒 | Switchyardの回答モデル | 秒 |
|---|---|---:|---|---:|
| simple | nvidia/nemotron-3-super-120b-a12b | 1.85 | nvidia/Nemotron-3_5-Lightning | 9.81 |
| deeper-reply | nvidia/Nemotron-3-Ultra-550b-a55b | 4.23 | nvidia/Nemotron-3_5-Lightning | 11.92 |
| thanks-only | nvidia/nemotron-3-super-120b-a12b | 1.57 | nvidia/Nemotron-3_5-Lightning | 7.09 |
| thanks-with-question | nvidia/nemotron-3-super-120b-a12b | 2.71 | nvidia/nemotron-3-super-120b-a12b | 8.15 |
| thanks-with-correction | nvidia/Nemotron-3-Ultra-550b-a55b | 3.87 | nvidia/Nemotron-3-Ultra-550b-a55b | 10.83 |

10/10回答成功。simpleの期待文字4は両方式で合格。

従来方式 平均2.84秒・中央値2.71秒。Switchyard 平均9.56秒・中央値9.81秒。

Switchyardの判定5回だけで入力2,152・出力9,746トークン。判定の思考コストと待ち時間が大きい。deepを意図したdeeper-replyでLightningが選ばれ、判定方針と不一致。

## 回答レビューと限界

simpleとthanks-onlyは簡潔。thanks-with-questionとthanks-with-correctionは両方式の本文が一致。deeper-replyは従来Ultraの方が長く、Switchyard Lightningは英語の問いへ日本語で応答。移動所要時間や公式情報への言及は検索・出典確認を実行していないため、両方式とも事実性の合格を判定できない。

各ケース1回のため統計的結論は出さない。価格表を指定していないためUSD費用は算定しない。Switchyard判定トークンも総費用に含める必要がある。

次の比較候補: 判定のreasoningを無効化した構成と現在構成を比較し、英語・日本語の再考要求でdeepへの選択を確認する。

## 試験構成

- Switchyard server: v0.3.0。
- 設定: [routes.toml](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/examples/switchyard/routes.toml)。
- 入力: [replay.jsonl](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/examples/switchyard/replay.jsonl)。
- 実行: [cmd/router-replay](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/cmd/router-replay/main.go) の `-mode both`。ケースごとに実行順序を交替。
- 従来方式: 通常はSuper。返信時にLightningが熟考の必要性を判定し、必要ならUltra。
- Switchyard: 各要求をLightningがlight（Lightning）・standard（Super）・deep（Ultra）に分類。

## 回答全文

各ケースの全文は折りたたみ内に保存しています。同一文面の回答は先に掲載した出力へリンクしています。`lightning` は従来方式の実行モード名であり、回答モデル名ではありません。移動所要時間などの記述は未検証の生成結果です。

### simple / lightning

<details>
<summary>回答全文を開く</summary>

```text
2 + 2 = 4
```

</details>

### simple / switchyard

<details>
<summary>回答全文を開く</summary>

```text
4
```

</details>

### deeper-reply / switchyard

<details>
<summary>回答全文を開く</summary>

```text
電車の乗り換えに10分を予算するというアドバイスは、健常者で移動に大きな支障がない場合の目安です。車椅子利用者や移動に時間がかかる人にとっては、以下の理由で十分とは言えません。

**想定する制約と前提**
1. **バリアフリー設備の有無**：駅にエレベーターやエスカレーターが完備されているか、あるいは階段しかない場合の待ち時間。
2. **車両間の移動距離**：乗り換え駅のホーム間距離が長く、移動に要する時間が10分を超えるケース。
3. **車椅子や補助具の取り扱い**：車椅子の折りたたみ、荷物の積み下ろし、乗降口までの距離などに要する時間。
4. **混雑状況**：ラッシュ時やイベント時の混雑により、改札やホームでの待ち時間が増える。
5. **個人の体力・健康状態**：疲労や痛みがある場合、同じ距離でも時間がかかる。

**調整の目安**
- 最低でも**15〜20分**の余裕を持たせる。
- 事前に駅のバリアフリー地図を確認し、エレベーターの位置や稼働状況をチェックする。
- 必要に応じて駅係員やバリアフリー対応サービスの利用を検討する。

以上の点を踏まえ、移動に時間がかかる人に対しては10分では不十分であり、余裕を持ったスケジュール組みが推奨されます。
```

</details>

### deeper-reply / lightning

<details>
<summary>回答全文を開く</summary>

```text
## Reconsidered Advice for Limited Mobility

**Revised recommendation: Budget 20–30 minutes minimum, and up to 45 minutes at large or unfamiliar stations.**

---

### Assumptions in the original "10 minutes" advice
The 10-minute rule assumes:
1. **Normal walking speed** — no mobility aid, fatigue, pain, or balance issues.
2. **Direct, level access** — platforms are connected by stairs/escalators that are right next to each other.
3. **Elevators/lifts are immediately available** — no waiting, no detours, no out-of-service units.
4. **No assistance needed** — can board/alight independently, no ramp request, no staff coordination.
5. **Crowd navigation is easy** — can move through dense flow, step aside quickly, react to sudden stops.
6. **Signage is instantly readable** — no visual/cognitive processing delay.
7. **Train runs on time and on the expected platform** — no last-minute platform changes requiring re-routing.
8. **No extra logistics** — no luggage, medical equipment, service animal, or accessible-toilet stop.

---

### Why these fail for limited mobility
- **Elevator detours** often add 5–15 minutes each way (finding it, waiting, longer path).
- **Staff-assisted boarding** (ramps, wheelchair spaces) typically requires 5–10 minutes advance notice and wait time.
- **Slower movement** through corridors, ticket gates, and crowds adds 50–100% more transit time.
- **Platform changes** mean repeating the elevator hunt on a new route.
- **Fatigue/anxiety** reduce sustainable pace and increase need for rest pauses.

---

### Practical adjusted plan
| Step | Extra time vs. 10 min baseline |
|------|-------------------------------|
| Locate & reach first elevator | +5–10 min |
| Wait for / ride elevator | +3–8 min |
| Longer accessible corridor route | +3–7 min |
| Request ramp / boarding help | +5–10 min (pre-book if possible) |
| Buffer for platform change / crowd / rest | +5–10 min |
| **Total** | **≈ 20–45 min** |

---

### Actionable tips
1. **Check station accessibility maps** in advance (operator website/app) — note elevator locations, accessible gates, toilet positions.
2. **Pre-book assistance** (most rail operators offer 24 h–1 h advance booking for ramps, guidance, wheelchair spaces).
3. **Identify a backup elevator** in case the primary is out of service.
4. **Add 10 minutes** to whatever the journey planner suggests for "transfer time."
5. **Carry a small card** with your assistance needs / destination platform — helps staff help you faster.

**Bottom line:** 10 minutes is a "fit, unencumbered, familiar-station" baseline. For limited mobility, treat 20 minutes as the new floor and plan upward from there.
```

</details>

### thanks-only / lightning

<details>
<summary>回答全文を開く</summary>

```text
どういたしまして！
```

</details>

### thanks-only / switchyard

本文は [thanks-only / lightning](#thanks-only--lightning) と同一。

### thanks-with-question / switchyard

<details>
<summary>回答全文を開く</summary>

```text
駅から近いラーメン店を選ぶポイントは以下の通りです。
1. 駅出口から徒歩5分以内を目安に地図アプリで距離を確認。
2. 改札周辺の案内板や駅構内の飲食店フロアガイドを見ると、すぐ近くの店が分かります。
3. 口コミサイトで「駅近」「徒歩○分」などのキーワードで絞り込み、実際の訪問時間や混雑状況もチェック。
4. 雨の日でも濡れにくい屋根がある通路や地下街直結の店だと便利です。
これらを参考に、目的の駅周辺で評判の良いラーメン店を見つけてみてください。
```

</details>

### thanks-with-question / lightning

本文は [thanks-with-question / switchyard](#thanks-with-question--switchyard) と同一。

### thanks-with-correction / lightning

<details>
<summary>回答全文を開く</summary>

```text
10分は**一般的な目安**であり、足が不自由な方には**不十分なことが多い**です。根拠と補足を整理します。

**なぜ「10分」と言われるか**
- JR東日本などの公式案内や乗換案内アプリが「同一駅・同一ホームなら2〜3分、階段・改札移動ありで5〜10分」と提示しているため。
- バリアフリールート（エレベーター・スロープ）は**一般ルートと動線が異なる**ことが多く、移動距離が伸びます。

**足が不自由な方が見るべきポイント**
1. **エレベーターの位置・台数** → ホーム端にしかない、改札外にしかない、1基しかない等で大幅に時間がかかる。
2. **バリアフリールートの経路長** → 一般ルートの1.5〜3倍の距離になる駅もある（例：東京メトロ丸ノ内線→JR山手線の一部乗換など）。
3. **混雑時の待ち時間** → エレベーター1基待ちで数分、車椅子優先でさらに待つ可能性。
4. **駅係員の介助手配** → 事前連絡なしだと到着後に呼び出し・誘導で5〜10分追加。

**実務的な目安（車椅子・杖・歩行困難）**
- 同一駅・同一フロア直結：**15〜20分**
- 改札・フロア移動あり：**25〜35分**
- 大規模ターミナル（東京・新宿・大阪等）：**30〜45分**

**確認手段**
- **駅別バリアフリー情報**（JR「駅構内図・バリアフリー情報」、メトロ「駅バリアフリー情報」）でエレベーター位置・経路図を事前確認。
- **乗換案内アプリ**（JR東日本アプリ、Yahoo!乗換案内、Googleマップ等）で「車椅子・ベビーカー優先」モードをONにして所要時間を出す。
- **駅係員への事前連絡**（前日〜当日朝）で介助ルート・所要時間を確定させるのが最確実。

結論：**「10分」は健常者向けの最速値。足が不自由な方は**最低20分、主要駅なら30分以上**を見込み、駅公式図面・アプリのバリアフリーモードで個別検証してください。
```

</details>

### thanks-with-correction / switchyard

本文は [thanks-with-correction / lightning](#thanks-with-correction--lightning) と同一。
