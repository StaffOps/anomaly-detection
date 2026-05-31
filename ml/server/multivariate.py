"""Isolation Forest multivariate anomaly detection."""

import numpy as np
from sklearn.ensemble import IsolationForest


class MultivariateDetector:
    def __init__(self, contamination: float = 0.05):
        self.model = IsolationForest(contamination=contamination, random_state=42)
        self._history: list[dict[str, float]] = []
        self._fitted = False
        self._min_samples = 50

    def detect(self, samples: dict[str, float]) -> dict:
        self._history.append(samples)

        if len(self._history) < self._min_samples:
            return {"is_anomaly": False, "score": 0.0, "contributors": []}

        # Fit on history
        keys = sorted(samples.keys())
        X = np.array([[h.get(k, 0.0) for k in keys] for h in self._history])

        if not self._fitted or len(self._history) % 100 == 0:
            self.model.fit(X)
            self._fitted = True

        # Score the latest sample
        current = np.array([[samples.get(k, 0.0) for k in keys]])
        score = -self.model.score_samples(current)[0]  # higher = more anomalous
        is_anomaly = self.model.predict(current)[0] == -1

        # Find contributing metrics (deviation from mean)
        contributors = []
        if is_anomaly:
            mean = X[:-1].mean(axis=0)
            std = X[:-1].std(axis=0) + 1e-9
            z_scores = np.abs((current[0] - mean) / std)
            top_indices = z_scores.argsort()[-3:][::-1]
            contributors = [keys[i] for i in top_indices if z_scores[i] > 2.0]

        return {"is_anomaly": is_anomaly, "score": float(score), "contributors": contributors}
