import { panel, room } from "./frame.js";
import { nobody, person, personas } from "./fixtures.js";

export default { title: "Room" };

const both = (rows, options) => {
  const view = room(rows, options);
  const host = document.createElement("div");
  host.append(view.banner, view.bar);
  return panel(host);
};

export const Empty = { name: "誰もいない（呼ぶ導線つき）", render: () => both(nobody) };
export const Present = { name: "4体いる", render: () => both(personas) };
export const Speaking = { name: "読み上げ中", render: () => both(personas, { speaking: true }) };
export const PartlyOn = {
  name: "1体だけ有効",
  render: () => both(personas.map((row, index) => ({ ...row, active: index === 3 }))),
};
export const WithPeople = {
  name: "関心ごとと人物がいる",
  render: () => both([...personas, person(), { ...person(), id: "tanaka", name: "田中", icon: "🧑‍💼" }, { ...person(), id: "suzuki", name: "鈴木", icon: "👩" }]),
};
