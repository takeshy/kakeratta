// Native HTML/CSS gallery layouts using the existing English UI captures.
// Run from any directory: node docs/hackathon/gallery/build.mjs
// The generated HTML is scratch output; upload the numbered PNG files.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '../../..');
const scratch = join(root, 'tmp/hackathon-gallery');
mkdirSync(scratch, { recursive: true });
mkdirSync(join(here, 'images'), { recursive: true });
const escape = s => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;');
const asset = name => `data:image/png;base64,${readFileSync(join(root, 'docs/captures/en', `${name}.png`)).toString('base64')}`;
// The original pixels remain unchanged. CSS selects a rectangular UI excerpt.
function shot(name, x, y, w, h, width, label = '') {
  const scale = width / w;
  return `<figure style="width:${width}px">${label ? `<figcaption>${label}</figcaption>` : ''}<div class="shot" style="width:${width}px;height:${h * scale}px"><img alt="${escape(name)}" src="${asset(name)}" style="position:absolute;width:${readFileSync(join(root, 'docs/captures/en', `${name}.png`)).readUInt32BE(16) * scale}px;max-width:none;left:${-x * scale}px;top:${-y * scale}px"></div></figure>`;
}
const placed = (x, y, content) => `<div class="placed" style="left:${x}px;top:${y}px">${content}</div>`;
const tag = text => `<span class="tag">${text}</span>`;
const note = (title, body) => `<div class="note"><strong>${title}</strong><p>${body}</p></div>`;
const header = (eyebrow, title, subtitle = '') => `<header><div class="eyebrow">${eyebrow}</div><h1>${title}</h1>${subtitle ? `<p class="subtitle">${subtitle}</p>` : ''}</header>`;
const slides = [
  {
    name: '01-remember-what-matters', title: 'A personal AI timeline that remembers what matters',
    caption: 'Post fragments of your day as text, photos, URLs or speech. Dedicated personas connect them with what you shared before.',
    sources: ['timeline'],
    body: `<div class="hero-copy"><div class="eyebrow">YOUR DAY, IN FRAGMENTS</div><h1>A little note.<br>A lasting<br><em>connection.</em></h1><p class="lead">A personal AI timeline that remembers<br>what matters to you.</p><div class="tags">${tag('Text')}${tag('Photos')}${tag('URLs')}${tag('Voice')}</div><p class="hero-small">Post naturally. Let dedicated personas<br>connect today with what came before.</p></div>${placed(1020, 200, shot('timeline', 28, 852, 724, 1084, 610))}<div class="side-label">ONE FRAGMENT → A PERSONAL RESPONSE</div>`,
  },
  {
    name: '02-the-right-moment', title: 'Bring back a forgotten plan at the right moment',
    caption: 'A missed restaurant visit becomes useful again when you mention another trip. Personas can surface relevant memories during check-ins.',
    sources: ['travel-memory'],
    body: `${header('01 / REMEMBERING', 'The right memory.<br><em>At the right moment.</em>', 'A restaurant you missed. A new trip. A helpful connection.')}<div class="recall-label" style="left:94px;top:410px">THEN · A MISSED CHANCE</div>${placed(94, 455, shot('travel-memory', 28, 1708, 724, 432, 660))}<div class="recall-label" style="left:94px;top:885px">NOW · A NEW TRIP</div>${placed(94, 930, shot('travel-memory', 28, 1304, 724, 185, 660))}<div class="big-arrow">→</div><div class="recall-label" style="left:960px;top:410px">LATER · YOUR PERSONA CONNECTS THE TWO</div>${placed(960, 455, shot('travel-memory', 28, 726, 724, 550, 740))}${placed(982, 1060, '<p class="closing">Remembering includes bringing it back.</p>')}`,
  },
  {
    name: '03-personas-for-your-life', title: 'Create personas for the people and interests in your life',
    caption: 'Describe a persona in plain language. Each person or interest can have its own memory, instructions and tools.',
    sources: ['persona-create', 'people'],
    body: `${header('02 / PERSONALIZATION', 'Your people.<br>Your interests.<br><em>Their own memory.</em>')}<div class="left-notes">${note('Describe it in your own words', 'Create a persona for wine, books,<br>a friend, or anything you care about.')}${note('Keep the context personal', 'Each persona remembers the fragments<br>that belong to its person or interest.')}</div>${placed(780, 220, shot('persona-create', 24, 26, 716, 1132, 440, 'CREATE IN PLAIN LANGUAGE'))}${placed(1260, 370, shot('people', 30, 1620, 720, 696, 440, 'A PERSON WITH THEIR OWN MEMORY'))}`,
  },
  {
    name: '04-memory-you-control', title: 'Inspect, trace and correct what your AI remembers',
    caption: 'Open a persona’s memory, follow facts back to their original posts, edit them or forget them. Original timeline posts stay intact.',
    sources: ['memory'],
    body: `${header('03 / MEMORY YOU CONTROL', 'See what it remembers.<br><em>Change what it gets wrong.</em>', 'Facts, records and source links make memory something you can inspect.')}<div class="memory-notes">${note('Trace', 'Follow “Original post” to the conversation<br>behind a memory.')}${note('Correct', 'Update a fact as your preferences change.')}${note('Forget', 'Remove a memory without deleting<br>the original timeline post.')}</div>${placed(1010, 290, shot('memory', 52, 430, 660, 860, 450, 'A PERSONA’S MEMORY · PREFERENCE'))}`,
  },
  {
    name: '05-routing-and-feedback', title: 'Understand who responds and refine the behavior',
    caption: 'Inspect which personas received a post and why others did not. Adjust routing conditions or preview a revised reply before applying it.',
    sources: ['routing', 'reply-trial'],
    body: `${header('04 / VISIBLE DECISIONS', 'The right persona.<br><em>A response you can refine.</em>', 'See who was called, why others were skipped, and improve the next response.')} ${placed(100, 410, shot('routing', 24, 26, 716, 920, 515, 'WHY THIS PERSONA?'))}${placed(850, 450, shot('reply-trial', 24, 26, 716, 656, 680, 'PREVIEW A REVISED REPLY'))}`,
  },
  {
    name: '06-find-the-fragment', title: 'Find saved fragments by words or meaning',
    caption: 'Search your timeline by keyword, meaning, date or persona, and keep useful posts and replies in favorites.',
    sources: ['search-semantic'],
    body: `${header('05 / FIND IT AGAIN', 'You remember the feeling.<br><em>Find the fragment.</em>', 'Search by words or meaning. Keep useful posts and answers in favorites.')} ${placed(98, 440, shot('search-semantic', 26, 28, 728, 345, 700, 'A NATURAL-LANGUAGE QUERY'))}${placed(98, 855, '<div class="search-note">“A light red wine I enjoyed”<span>Find the note even when you<br>do not recall the wine’s name.</span></div>')}${placed(920, 440, shot('search-semantic', 28, 858, 724, 665, 700, 'RELATED FRAGMENTS'))}`,
  },
  {
    name: '07-connected-agents', title: 'Connect external agents to your personal timeline',
    caption: 'Hermes or Obsidian’s LLM Connect Hub can research and answer through MCP. kakeratta keeps the timeline, schedules and memory updates.',
    sources: ['llm-connect-hub'],
    body: `${header('06 / CONNECTED AGENTS', 'Bring your agents.<br><em>Keep the conversation.</em>')}<div class="left-notes integrations">${note('Obsidian LLM Connect Hub', 'Bring relevant Vault knowledge<br>into a persona’s response.')}${note('Hermes via MCP', 'Let an external agent research and answer<br>through the same timeline.')}<div class="tags">${tag('MCP')}${tag('Obsidian')}${tag('Hermes')}</div></div>${placed(945, 310, shot('llm-connect-hub', 28, 28, 708, 780, 640, 'AN EXTERNAL AGENT, IN THE SAME THREAD'))}`,
  },
  {
    name: '08-powered-by-nemotron', title: 'NVIDIA Nemotron on Nebius Token Factory',
    caption: 'Super routes each post and handles everyday answers and notes; Super VL plans schedules and runs the daily check-ins. Ultra composes deep answers. A tagged record is added after publication; records are folded into grounded facts at check-in.',
    sources: [],
    body: `${header('07 / UNDER THE HOOD', 'Personal memory.<br><em>Purposeful AI.</em>', 'Powered by NVIDIA Nemotron on Nebius Token Factory.')}<div class="engine"><div class="model"><div class="model-kicker">EVERYDAY REASONING</div><h2>Nemotron Super</h2><p>Routing · ordinary replies<br>Research · memory notes</p></div><div class="model ultra"><div class="model-kicker">DEEP ANSWERS</div><h2>Nemotron Ultra</h2><p>Final answers for<br>deep requests</p></div></div><div class="pipeline">${['Save fragment', 'Route & research', 'Compose answer', 'Plan & check', 'Execute & publish', 'Record fragment'].map((s,i)=>`<div class="stage"><span>0${i+1}</span>${s}</div>`).join('<b>→</b>')}</div><div class="engine-notes">${note('Durable work', 'Saved jobs and retries keep processing recoverable.')}${note('Memory at check-in', 'Fold records into facts, retaining useful changes and sources.')}${note('Reviewed schedules', 'Schedule changes are checked before execution.')}</div>`,
  },
  {
    name: '09-monthly-reflection', title: 'A month of fragments, connected',
    caption: 'Monthly summaries bring people, food and unfinished plans together with links back to the original posts.', sources: ['month-summary'],
    body: `${header('LOOK BACK', 'Small moments.<br><em>A bigger picture.</em>')}<div class="left-notes">${note('Reflect on your month', 'See recurring themes and plans that are still open.')}${note('Follow the source', 'Return to the posts behind each summary.')}</div>${placed(1030, 330, shot('month-summary', 24, 26, 716, 800, 600, 'A MONTH, ORGANIZED BY TOPIC'))}`,
  },
  {
    name: '10-save-useful-answers', title: 'Keep useful posts and answers in favorites',
    caption: 'Favorite both your own posts and persona replies, then return to them from the saved-entry view.', sources: ['favorites'],
    body: `${header('KEEP WHAT HELPS', 'A useful answer.<br><em>Ready when you need it.</em>')}<div class="left-notes">${note('Save either side', 'Keep a question, a reply, or a small detail.')}${note('Find it again', 'Browse favorites when you need that advice again.')}</div>${placed(1030, 330, shot('favorites', 28, 26, 724, 1100, 480, 'YOUR SAVED FRAGMENTS'))}`,
  },
  {
    name: '11-offline-fragments', title: 'Capture a thought even while offline',
    caption: 'Write while disconnected. Posts stay in this browser’s outbox until the connection returns and delivery can resume.', sources: ['offline'],
    body: `${header('IN THE MOMENT', 'The thought comes now.<br><em>The connection can wait.</em>')}<div class="left-notes">${note('Catch the thought', 'Write the fragment while it is still fresh.')}${note('Resume delivery', 'Reconnect and retry the posts waiting in the outbox.')}</div>${placed(1030, 330, shot('offline', 28, 26, 724, 810, 650, 'DISCONNECTED · SAVED ON THIS DEVICE'))}`,
  },
  {
    name: '12-share-your-selection', title: 'Preview exactly what you share to Bluesky',
    caption: 'Choose text or image for each selected message, inspect the result inline, and press Post to publish the conversation to Bluesky.', sources: ['bluesky-preview'],
    body: `${header('SELECTIVE SHARING', 'Choose the pieces.<br><em>Preview the conversation.</em>')}<div class="left-notes">${note('Choose what goes out', 'Include only the parts you want to publish.')}${note('Text or image', 'Choose a format for each message<br>and inspect the result before pressing Post.')}</div>${placed(1030, 330, shot('bluesky-preview', 24, 700, 716, 1050, 450, 'TEXT OR IMAGE · PREVIEW INLINE'))}${placed(1030, 1050, shot('bluesky-preview', 24, readFileSync(join(root, 'docs/captures/en/bluesky-preview.png')).readUInt32BE(20) - 160, 716, 120, 450))}`,
  },
  {
    name: '13-understand-ai-usage', title: 'See what each persona costs and what drives it',
    caption: 'Inspect each persona’s monthly AI costs and web search counts, split into replies, scheduled runs and other work. Compare them with the overall total, including shared processing.', sources: ['persona-usage', 'settings-usage'],
    body: `${header('VISIBLE USAGE', 'Know each persona’s cost.<br><em>See what drives it.</em>')}<div class="left-notes usage-notes">${note('A breakdown for each persona', 'See the cost of replies,<br>scheduled runs and other work.')}${note('Search counts, too', 'Understand the activity behind<br>each persona’s monthly total.')}${note('The whole picture', 'Overall totals also include<br>routing and shared processing.')}</div>${placed(590, 375, shot('persona-usage', 24, 24, 716, 120, 580, 'ONE PERSONA · SHIORI'))}${placed(590, 515, shot('persona-usage', 24, 625, 716, 695, 580))}${placed(1250, 375, shot('settings-usage', 24, 26, 684, 660, 450, 'OVERALL MONTHLY TOTAL'))}${placed(1250, 955, '<div class="usage-context">Costs are estimates in USD.<br>Amounts and counts shown are examples.</div>')}`,
  },
  {
    name: '14-turn-notes-into-infographics', title: 'Turn notes and conversations into infographics',
    caption: 'Ask the infographic persona to turn existing notes, articles or conversations into visual cards with emoji and Mermaid diagrams. Powered by Nemotron Ultra.', sources: [],
    body: `${header('MAKE IT VISUAL', 'Connected ideas.<br><em>A picture you can follow.</em>')}<div class="left-notes">${note('Ask in the conversation', 'Reply with @infographic to visualize an answer<br>or the material already in your thread.')}${note('See the connections', 'Emoji cards and diagrams turn the details<br>into a clear overview.')}<div class="tags">${tag('Markdown')}${tag('Mermaid')}${tag('Nemotron Ultra')}</div></div>${placed(815, 330, '<figure><figcaption>FROM A TRAVEL CONVERSATION</figcaption><img style="width:890px;border-radius:22px;box-shadow:0 18px 45px #3d362515" src="data:image/png;base64,'+readFileSync(join(here, 'infographic-example.png')).toString('base64')+'" alt="English infographic with memory cards and a trip-planning flowchart"></figure>')}`,
  },
  {
    name: '15-remember-your-people', title: 'Keep the small details about your people',
    caption: 'A person’s timeline connects shared meals, preferences and upcoming occasions, helping small details stay useful.', sources: ['timeline-speaker'],
    body: `${header('PERSONAL CONTEXT', 'A friend’s favorites.<br><em>A thoughtful next step.</em>')}<div class="left-notes">${note('Keep the human details', 'A wine they liked. A meal you shared. An occasion coming up.')}${note('Connect past and future', 'Use the history to make your next conversation more thoughtful.')}</div>${placed(1030, 330, shot('timeline-speaker', 28, 980, 724, 1580, 335, 'FRAGMENTS ABOUT ONE PERSON'))}`,
  },
];

const css = `
@font-face{font-family:Gallery;src:local('Lato')}@font-face{font-family:Gallery;src:local('Lato Heavy');font-weight:800}
*{box-sizing:border-box}html,body{margin:0;padding:0}body{font-family:Gallery,'Arial',sans-serif;color:#282b27;background:#efece5}
.slide{position:relative;width:1800px;height:1200px;overflow:hidden;background:#f6f3ed}
.slide:before{content:'';position:absolute;right:-190px;top:-360px;width:1060px;height:1060px;border-radius:50%;background:#e9e8db;opacity:.6}
.brand{position:absolute;left:94px;top:52px;display:flex;align-items:center;gap:14px;font-size:30px;font-weight:800;letter-spacing:-1px}
.brand img{width:42px;height:42px;object-fit:contain}.brand svg{width:145px;height:30px}.edition{position:absolute;right:96px;top:65px;font-size:18px;letter-spacing:3px;color:#6c7168}
footer{position:absolute;left:94px;right:94px;bottom:32px;border-top:1px solid #d6d7ca;padding-top:19px;display:flex;justify-content:space-between;font-size:18px;color:#727469}
header{position:absolute;left:94px;top:166px;max-width:1570px}.eyebrow{font-size:19px;font-weight:800;letter-spacing:3px;color:#94623b;margin-bottom:24px}
h1{font-size:68px;line-height:1.04;letter-spacing:-2.8px;font-weight:800;margin:0}em{font-style:normal;color:#566e51}.subtitle{font-size:27px;line-height:1.4;margin-top:20px;color:#64685e;max-width:1460px}
.slide-4 .subtitle{max-width:820px}.placed{position:absolute}figure{margin:0}figcaption,.recall-label{font-size:18px;letter-spacing:1.6px;font-weight:800;color:#66705f;margin-bottom:19px}.shot{position:relative;overflow:hidden;border-radius:22px;background:#fffdfa;box-shadow:0 18px 45px #3d362515,0 0 0 1px #d9d7ca}.shot img{display:block}
.usage-notes{top:480px;width:440px}.usage-notes .note strong{font-size:26px}.usage-notes .note p{font-size:24px}.usage-context{font-size:22px;line-height:1.5;color:#666b60}
.hero-copy{position:absolute;top:245px;left:94px}.hero-copy h1{font-size:112px;line-height:1.02;letter-spacing:-5px}.lead{font-size:36px;line-height:1.35;margin:34px 0;color:#565f52}.hero-small{font-size:26px;line-height:1.45;color:#6d7065;margin-top:36px}.tag{display:inline-block;padding:11px 22px;border:1px solid #ced2c3;border-radius:28px;font-size:21px;color:#53644a;background:#edf0e6}.tags{display:flex;gap:12px}.side-label{position:absolute;left:1020px;top:157px;font-size:18px;letter-spacing:1.8px;color:#67745d;font-weight:800}
.recall-label{position:absolute}.big-arrow{position:absolute;left:794px;top:675px;font-size:80px;color:#a2ae90}.closing{font-size:25px;color:#566e51;margin:0}.left-notes{position:absolute;left:96px;top:620px;width:620px}.note{margin-bottom:40px}.note strong{font-size:29px;font-weight:800;color:#3f5338}.note p{font-size:26px;line-height:1.45;color:#666b60;margin:12px 0 0}.memory-notes{position:absolute;left:120px;top:490px}.memory-notes .note{border-left:4px solid #c1ccb0;padding-left:27px;margin-bottom:43px}.search-note{font-size:33px;font-weight:800;color:#53684a;border-left:4px solid #c1ccb0;padding-left:28px}.search-note span{display:block;margin-top:20px;font-size:25px;font-weight:400;line-height:1.45;color:#656c5f}.integrations{top:565px}.integrations .tags{margin-top:44px}
.engine{position:absolute;left:94px;right:94px;top:445px;display:flex;gap:28px}.model{background:#e5eadc;border:1px solid #cbd5bf;border-radius:26px;padding:35px 42px;width:50%}.model.ultra{background:#eee5d8;border-color:#ddccb5}.model-kicker{font-size:17px;font-weight:800;letter-spacing:2px;color:#69745e}.model h2{font-size:44px;letter-spacing:-1px;margin:17px 0}.model p{font-size:27px;line-height:1.5;margin:0;color:#5c6555}.pipeline{position:absolute;left:94px;right:94px;top:785px;display:flex;align-items:center;justify-content:space-between;gap:12px}.stage{height:126px;width:238px;background:#fffdfa;border:1px solid #dfded4;border-radius:17px;padding:19px 20px;font-size:24px;font-weight:800;line-height:1.3}.stage span{display:block;font-size:17px;color:#9b7347;margin-bottom:12px}.pipeline b{color:#9aaa8d;font-size:27px}.engine-notes{position:absolute;left:94px;right:94px;top:965px;display:flex;gap:60px}.engine-notes .note{flex:1}.engine-notes strong{font-size:24px}.engine-notes p{font-size:21px;margin-top:9px}
`;
const wordmark = readFileSync(join(root, 'frontend/public/wordmark.svg'), 'utf8');
const icon = `data:image/png;base64,${readFileSync(join(root, 'frontend/public/icon.png')).toString('base64')}`;
function slideHTML(slide, i) {
  return `<article class="slide slide-${i+1}"><div class="brand"><img src="${icon}" alt="">${wordmark}</div><div class="edition">LIFE, REMEMBERED.</div>${slide.body}<footer><span>kakeratta.net</span><span>${String(i+1).padStart(2,'0')} / ${String(slides.length).padStart(2,'0')}</span></footer></article>`;
}
for (const [i, slide] of slides.entries()) {
  writeFileSync(join(scratch, `${slide.name}.html`), `<!doctype html><html lang="en"><meta charset="utf-8"><title>${slide.title}</title><style>${css}</style>${slideHTML(slide,i)}</html>`);
}
writeFileSync(join(here, 'captions.json'), JSON.stringify(slides.map(({name,title,caption,sources})=>({file:`images/${name}.png`,title,caption,sources:name.startsWith('14-') ? ['infographic-example.html', 'infographic-example.png', '../../../frontend/src/infographic.js'] : sources.map(s=>`../../captures/en/${s}.png`)})), null, 2)+'\n');
writeFileSync(join(here, 'preview.html'), `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>kakeratta — Hackathon gallery</title><style>body{font:18px/1.5 system-ui;background:#f6f3ed;color:#282b27;max-width:1200px;margin:auto;padding:32px}img{width:100%;height:auto;border-radius:12px}figure{margin:32px 0 60px}h1{font-size:32px}figcaption{margin-top:16px;color:#55634d}</style><h1>kakeratta — Hackathon gallery</h1><p>${slides.length} images · 1800 × 1200 · PNG · upload in numbered order</p>${slides.map(s=>`<figure><a href="images/${s.name}.png"><img src="images/${s.name}.png" alt="${escape(s.title)}"></a><figcaption><strong>${s.title}</strong><br>${s.caption}</figcaption></figure>`).join('')}</html>`);
console.log(`Generated ${slides.length} gallery layouts in ${scratch}`);
