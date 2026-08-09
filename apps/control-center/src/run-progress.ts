function jobUpdateSignature(job: any = {}) {
  const id = clean(job.id);
  if (!id) {
    return "";
  }
  return [
    id,
    clean(job.state),
    clean(job.progress),
    clean(job.failure),
    clean(job.updated)
  ].join("\n");
}

function nextJobUpdate(previousSignature, job: any = {}) {
  const signature = jobUpdateSignature(job);
  return {
    changed: Boolean(signature && signature !== String(previousSignature || "")),
    signature
  };
}

function clean(value) {
  return String(value || "").trim();
}

module.exports = {
  jobUpdateSignature,
  nextJobUpdate
};
