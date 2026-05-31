"""Prophet-based time series forecasting."""

import numpy as np
import pandas as pd
from prophet import Prophet


class Forecaster:
    def predict(
        self,
        values: list[float],
        timestamps: list[int],
        horizon_minutes: int,
        breach_threshold: float,
    ) -> dict:
        df = pd.DataFrame({
            "ds": pd.to_datetime(timestamps, unit="s"),
            "y": values,
        })

        model = Prophet(interval_width=0.95, daily_seasonality=True, weekly_seasonality=True)
        model.fit(df)

        future = model.make_future_dataframe(periods=horizon_minutes, freq="min")
        forecast = model.predict(future)

        predicted = forecast["yhat"].iloc[-horizon_minutes:].tolist()
        upper = forecast["yhat_upper"].iloc[-horizon_minutes:].tolist()
        lower = forecast["yhat_lower"].iloc[-horizon_minutes:].tolist()

        # Check if forecast breaches threshold
        will_breach = any(v > breach_threshold for v in upper)
        time_to_breach = 0.0
        if will_breach:
            for i, v in enumerate(predicted):
                if v > breach_threshold:
                    time_to_breach = i / 60.0  # minutes to hours
                    break

        confidence = 1.0 - np.mean(np.array(upper) - np.array(lower)) / (np.mean(values) + 1e-9)
        confidence = max(0.0, min(1.0, confidence))

        return {
            "predicted": predicted,
            "upper_bound": upper,
            "lower_bound": lower,
            "will_breach": will_breach,
            "time_to_breach": time_to_breach,
            "confidence": confidence,
        }
