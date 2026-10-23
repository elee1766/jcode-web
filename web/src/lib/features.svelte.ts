import { sessions as api } from "./client";

class Features {
  summaries = $state(false);
  summaryModel = $state("");

  async load() {
    try {
      const f = await api.getFeatures({});
      this.summaries = f.summaries;
      this.summaryModel = f.summaryModel;
    } catch {
      // Older server or offline. Leave features off.
    }
  }
}

export const features = new Features();
