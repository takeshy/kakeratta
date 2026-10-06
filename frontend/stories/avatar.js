// Small JPEG stand-ins keep profile examples self-contained.
export function sampleAvatar(letter = "K", background = "#7999a8") {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 128;
  const context = canvas.getContext("2d");
  context.fillStyle = background; context.fillRect(0, 0, 128, 128);
  context.fillStyle = "#fff"; context.font = "bold 64px sans-serif";
  context.textAlign = "center"; context.textBaseline = "middle";
  context.fillText(letter, 64, 68);
  return canvas.toDataURL("image/jpeg", 0.82);
}
