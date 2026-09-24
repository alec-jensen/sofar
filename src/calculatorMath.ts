export function monthsToSave(target: number, saved: number, monthly: number) {
  const remaining = Math.max(0, target - saved);
  return remaining === 0 ? 0 : monthly > 0 ? Math.ceil(remaining / monthly) : null;
}

export function emergencyRunway(cash: number, monthlyEssentials: number) {
  if (monthlyEssentials <= 0) return null;
  return {
    months: cash / monthlyEssentials,
    threeMonthGap: Math.max(0, monthlyEssentials * 3 - cash),
    sixMonthGap: Math.max(0, monthlyEssentials * 6 - cash),
  };
}

export function debtPayoff(
  balance: number,
  annualRatePercent: number,
  monthlyPayment: number,
) {
  if (balance <= 0) return { months: 0, interest: 0 };
  if (monthlyPayment <= 0 || annualRatePercent < 0) return null;
  let remaining = balance;
  let interestTotal = 0;
  for (let month = 1; month <= 600; month++) {
    const interest = Math.round((remaining * annualRatePercent) / 1200);
    if (monthlyPayment <= interest) return null;
    interestTotal += interest;
    remaining = Math.max(0, remaining + interest - monthlyPayment);
    if (remaining === 0) return { months: month, interest: interestTotal };
  }
  return null;
}

export function investmentGrowth(
  starting: number,
  monthly: number,
  annualRatePercent: number,
  years: number,
) {
  const months = years * 12;
  let value = starting;
  for (let month = 0; month < months; month++)
    value = value * (1 + annualRatePercent / 1200) + monthly;
  const contributed = starting + monthly * months;
  return { value: Math.round(value), contributed, growth: Math.round(value) - contributed };
}
