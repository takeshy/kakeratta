# Switchyard comparison — 2026-10-03

> Status (2026-10-03): The Switchyard integration, replay command, and sample configuration have been removed following this comparison. Answer routing has returned to the previous Super/Ultra behavior. This report is retained as a historical record; setup links point to the experiment commit.

[日本語](./switchyard-comparison-2026-10-03_ja.md)

Five shipped synthetic cases were run once per method, using Token Factory for both methods. The OpenRouter key was not used. The replay measures answer generation and the baseline's Lightning escalation decision. It runs no tools, searches, memory updates, or production writes.

## Setup

- Switchyard server: v0.3.0.
- Routing configuration: [routes.toml](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/examples/switchyard/routes.toml).
- Inputs: [replay.jsonl](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/examples/switchyard/replay.jsonl).
- Replay command: [cmd/router-replay](https://github.com/takeshy/kakeratta/blob/d7ae56de9315118d593b46d6dc2ae04f1ab22211/cmd/router-replay/main.go), using `-mode both` and alternating execution order by case.
- Baseline: Super answers by default; a Lightning decision on replies can escalate the answer to Ultra.
- Switchyard: Lightning classifies each request into light (Lightning), standard (Super), or deep (Ultra).

## Results

| Case | Baseline answer model | Seconds | Switchyard answer model | Seconds |
|---|---|---:|---|---:|
| simple | nvidia/nemotron-3-super-120b-a12b | 1.85 | nvidia/Nemotron-3_5-Lightning | 9.81 |
| deeper-reply | nvidia/Nemotron-3-Ultra-550b-a55b | 4.23 | nvidia/Nemotron-3_5-Lightning | 11.92 |
| thanks-only | nvidia/nemotron-3-super-120b-a12b | 1.57 | nvidia/Nemotron-3_5-Lightning | 7.09 |
| thanks-with-question | nvidia/nemotron-3-super-120b-a12b | 2.71 | nvidia/nemotron-3-super-120b-a12b | 8.15 |
| thanks-with-correction | nvidia/Nemotron-3-Ultra-550b-a55b | 3.87 | nvidia/Nemotron-3-Ultra-550b-a55b | 10.83 |

All 10 answers completed successfully. Both methods passed the expected-text check for `4` in the simple case.

Baseline mean latency was 2.84 seconds and median latency was 2.71 seconds. Switchyard mean latency was 9.56 seconds and median latency was 9.81 seconds. Switchyard was slower in every case in this run.

The five Switchyard classifier calls alone used 2,152 input tokens and 9,746 output tokens, including reasoning. The routing log shows substantial reasoning overhead and time spent on classification. Switchyard selected Lightning for `deeper-reply`, although the intended policy assigns reconsideration requests to deep/Ultra.

## Answer review and limits

The simple and thanks-only answers were concise. The answer text for thanks-with-question and thanks-with-correction was identical across both methods. For deeper-reply, the baseline Ultra answer was longer; Switchyard's Lightning answer used Japanese in response to an English question.

Neither method searched or verified sources. The generated claims about transfer times and official guidance therefore do not establish factual correctness. The original outputs below are evidence of model behavior, not verified travel guidance.

Each case was run only once, so these measurements do not support statistical conclusions. No price table was supplied and USD cost was not calculated. Any total-cost comparison must include Switchyard's classifier tokens as well as answer generation and the baseline's escalation calls.

A useful next experiment is to compare the current configuration with classifier reasoning disabled, then check whether English and Japanese reconsideration requests select deep/Ultra reliably.

## Original answer outputs

English translations accompany the Japanese outputs below. Each Japanese original is preserved in a collapsible block so the recorded language and wording can be checked. Translations reproduce the model’s claims without verifying them. The replay mode name `lightning` identifies the baseline method, not necessarily the model that answered.

### simple / lightning

```text
2 + 2 = 4
```

### simple / switchyard

```text
4
```

### deeper-reply / switchyard

**English translation:**

```text
The advice to budget 10 minutes for a train transfer is a guideline for people without disabilities who have no major difficulty moving around. It is not sufficient for wheelchair users or people who need more time to move, for the following reasons.

**Constraints and assumptions to consider**
1. **Availability of accessible facilities**: whether the station has elevators or escalators, or the waiting time when there are only stairs.
2. **Distance between vehicles**: platforms at a transfer station may be far enough apart that moving between them takes more than 10 minutes.
3. **Handling wheelchairs or mobility aids**: time needed to fold a wheelchair, load and unload luggage, or reach the boarding point.
4. **Crowding**: rush-hour or event crowds can increase waiting time at ticket gates and platforms.
5. **Individual stamina and health**: fatigue or pain can make the same distance take longer.

**Suggested adjustments**
- Allow at least **15–20 minutes**.
- Check the station's accessibility map in advance, including elevator locations and operating status.
- Consider assistance from station staff or accessibility services as needed.

For these reasons, 10 minutes is insufficient for people who need more time to move, and a schedule with additional time is recommended.
```

<details>
<summary>Original Japanese output</summary>

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

```text
## Reconsidered Advice for Limited Mobility

**Revised recommendation: Budget 20–30 minutes minimum, and up to 45 minutes at large or unfamiliar stations.**

---
```

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

```text
---
```

### Why these fail for limited mobility
- **Elevator detours** often add 5–15 minutes each way (finding it, waiting, longer path).
- **Staff-assisted boarding** (ramps, wheelchair spaces) typically requires 5–10 minutes advance notice and wait time.
- **Slower movement** through corridors, ticket gates, and crowds adds 50–100% more transit time.
- **Platform changes** mean repeating the elevator hunt on a new route.
- **Fatigue/anxiety** reduce sustainable pace and increase need for rest pauses.

```text
---
```

### Practical adjusted plan
| Step | Extra time vs. 10 min baseline |
|------|-------------------------------|
| Locate & reach first elevator | +5–10 min |
| Wait for / ride elevator | +3–8 min |
| Longer accessible corridor route | +3–7 min |
| Request ramp / boarding help | +5–10 min (pre-book if possible) |
| Buffer for platform change / crowd / rest | +5–10 min |
| **Total** | **≈ 20–45 min** |

```text
---
```

### Actionable tips
1. **Check station accessibility maps** in advance (operator website/app) — note elevator locations, accessible gates, toilet positions.
2. **Pre-book assistance** (most rail operators offer 24 h–1 h advance booking for ramps, guidance, wheelchair spaces).
3. **Identify a backup elevator** in case the primary is out of service.
4. **Add 10 minutes** to whatever the journey planner suggests for "transfer time."
5. **Carry a small card** with your assistance needs / destination platform — helps staff help you faster.

```text
**Bottom line:** 10 minutes is a "fit, unencumbered, familiar-station" baseline. For limited mobility, treat 20 minutes as the new floor and plan upward from there.
```

### thanks-only / lightning

**English translation:**

```text
You're welcome!
```

<details>
<summary>Original Japanese output</summary>

```text
どういたしまして！
```

</details>

### thanks-only / switchyard

**English translation:**

```text
You're welcome!
```

<details>
<summary>Original Japanese output</summary>

```text
どういたしまして！
```

</details>

### thanks-with-question / switchyard

**English translation:**

```text
Here are some points to consider when choosing a ramen restaurant near a station.
1. Use a map app to check the distance, aiming for a walk of no more than five minutes from the station exit.
2. Check signs around the ticket gates or station dining-floor guides to find nearby restaurants.
3. Filter review sites using terms such as “near the station” or “a ○-minute walk,” and check actual visiting times and crowding.
4. A restaurant reached by a covered walkway or directly connected to an underground shopping area is convenient on rainy days.
Use these points to find a well-reviewed ramen restaurant near your destination station.
```

<details>
<summary>Original Japanese output</summary>

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

**English translation:**

```text
Here are some points to consider when choosing a ramen restaurant near a station.
1. Use a map app to check the distance, aiming for a walk of no more than five minutes from the station exit.
2. Check signs around the ticket gates or station dining-floor guides to find nearby restaurants.
3. Filter review sites using terms such as “near the station” or “a ○-minute walk,” and check actual visiting times and crowding.
4. A restaurant reached by a covered walkway or directly connected to an underground shopping area is convenient on rainy days.
Use these points to find a well-reviewed ramen restaurant near your destination station.
```

<details>
<summary>Original Japanese output</summary>

```text
駅から近いラーメン店を選ぶポイントは以下の通りです。
1. 駅出口から徒歩5分以内を目安に地図アプリで距離を確認。
2. 改札周辺の案内板や駅構内の飲食店フロアガイドを見ると、すぐ近くの店が分かります。
3. 口コミサイトで「駅近」「徒歩○分」などのキーワードで絞り込み、実際の訪問時間や混雑状況もチェック。
4. 雨の日でも濡れにくい屋根がある通路や地下街直結の店だと便利です。
これらを参考に、目的の駅周辺で評判の良いラーメン店を見つけてみてください。
```

</details>

### thanks-with-correction / lightning

**English translation:**

```text
Ten minutes is a **general guideline** and is **often insufficient for people with mobility difficulties**. Here are the reasons and additional considerations.

**Why “10 minutes” is suggested**
- Official guidance from operators such as JR East and journey-planning apps gives “2–3 minutes within the same station and platform, or 5–10 minutes when stairs or ticket gates are involved.”
- **Accessible routes** using elevators and ramps often follow a **different path from the standard route**, increasing the distance.

**What people with mobility difficulties should check**
1. **Elevator locations and numbers** → It can take much longer if elevators are only at the end of a platform, outside the ticket gates, or if there is only one.
2. **Length of the accessible route** → At some stations, it can be 1.5–3 times the standard distance, such as certain transfers between the Tokyo Metro Marunouchi Line and the JR Yamanote Line.
3. **Waiting during busy periods** → Waiting for one elevator can take several minutes, with potentially more waiting because of wheelchair priority.
4. **Arranging station-staff assistance** → Without advance notice, calling staff and receiving guidance after arrival can add 5–10 minutes.

**Practical estimates (wheelchair, cane, or difficulty walking)**
- Direct connection within the same station and on the same floor: **15–20 minutes**
- Changes of ticket gates or floors: **25–35 minutes**
- Major terminals such as Tokyo, Shinjuku, or Osaka: **30–45 minutes**

**How to check**
- Use **station accessibility information**, such as JR's station maps and accessibility information or the metro's station accessibility information, to check elevator locations and route maps in advance.
- In **journey-planning apps**, such as the JR East app, Yahoo! Transit, or Google Maps, enable a “wheelchair/stroller priority” mode to calculate the time.
- **Contact station staff in advance**, from the previous day to the morning of travel, to confirm the assisted route and time required; this is the most reliable option.

Conclusion: **“10 minutes” is the fastest estimate for people without disabilities. People with mobility difficulties should allow at least 20 minutes, or 30 minutes or more at major stations**, and check their specific route using official station maps and accessibility modes in apps.
```

<details>
<summary>Original Japanese output</summary>

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

**English translation:**

```text
Ten minutes is a **general guideline** and is **often insufficient for people with mobility difficulties**. Here are the reasons and additional considerations.

**Why “10 minutes” is suggested**
- Official guidance from operators such as JR East and journey-planning apps gives “2–3 minutes within the same station and platform, or 5–10 minutes when stairs or ticket gates are involved.”
- **Accessible routes** using elevators and ramps often follow a **different path from the standard route**, increasing the distance.

**What people with mobility difficulties should check**
1. **Elevator locations and numbers** → It can take much longer if elevators are only at the end of a platform, outside the ticket gates, or if there is only one.
2. **Length of the accessible route** → At some stations, it can be 1.5–3 times the standard distance, such as certain transfers between the Tokyo Metro Marunouchi Line and the JR Yamanote Line.
3. **Waiting during busy periods** → Waiting for one elevator can take several minutes, with potentially more waiting because of wheelchair priority.
4. **Arranging station-staff assistance** → Without advance notice, calling staff and receiving guidance after arrival can add 5–10 minutes.

**Practical estimates (wheelchair, cane, or difficulty walking)**
- Direct connection within the same station and on the same floor: **15–20 minutes**
- Changes of ticket gates or floors: **25–35 minutes**
- Major terminals such as Tokyo, Shinjuku, or Osaka: **30–45 minutes**

**How to check**
- Use **station accessibility information**, such as JR's station maps and accessibility information or the metro's station accessibility information, to check elevator locations and route maps in advance.
- In **journey-planning apps**, such as the JR East app, Yahoo! Transit, or Google Maps, enable a “wheelchair/stroller priority” mode to calculate the time.
- **Contact station staff in advance**, from the previous day to the morning of travel, to confirm the assisted route and time required; this is the most reliable option.

Conclusion: **“10 minutes” is the fastest estimate for people without disabilities. People with mobility difficulties should allow at least 20 minutes, or 30 minutes or more at major stations**, and check their specific route using official station maps and accessibility modes in apps.
```

<details>
<summary>Original Japanese output</summary>

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
