interface Window {
  computeHop: any;
  computeHopCapabilityCatalog: any;
  computeHopDaemonAutostart: any;
  computeHopDeviceStatus: any;
  computeHopDeviceTargets: any;
  computeHopJobFailure: any;
  computeHopJobList: any;
  computeHopOutputPath: any;
  computeHopOutputRestore: any;
  computeHopRunRecovery: any;
  computeHopRunRequest: any;
  computeHopRunSummary: any;
  computeHopSuggestionPlan: any;
  computeHopWorkPolicy: any;
}

interface Document {
  getElementById(elementId: string): any;
}

declare namespace NodeJS {
  interface Process {
    resourcesPath: string;
  }
}
