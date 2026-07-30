function runWorkingDirectory(jobRequest) {
  return String(jobRequest?.workingDirectory || "").trim();
}

module.exports = {
  runWorkingDirectory
};
