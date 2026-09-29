import React from "react";
import { Plus, X } from "lucide-react";
import { Button } from "../components/UI";

export default function HeaderFields({ value, onChange, label = "响应头" }) {
  const pairs = Object.entries(value || {});
  return (
    <div className="composer-headers">
      <div className="composer-section-line">
        <strong>{label}</strong>
        <Button
          onClick={() => {
            let number = pairs.length + 1;
            while (Object.hasOwn(value || {}, `X-Header-${number}`)) number++;
            onChange({ ...value, [`X-Header-${number}`]: "" });
          }}
          icon={Plus}
          variant="ghost"
        >
          添加
        </Button>
      </div>
      {pairs.map(([key, val], index) => (
        <div className="composer-header-row" key={index}>
          <input
            aria-label={`${label}名称 ${index + 1}`}
            value={key}
            onChange={(e) =>
              onChange(
                Object.fromEntries(
                  pairs.map((pair, i) =>
                    i === index ? [e.target.value, pair[1]] : pair,
                  ),
                ),
              )
            }
          />
          <input
            aria-label={`${label}值 ${index + 1}`}
            value={val}
            onChange={(e) => onChange({ ...value, [key]: e.target.value })}
          />
          <button
            className="icon-button"
            aria-label={`删除${label} ${key}`}
            onClick={() =>
              onChange(Object.fromEntries(pairs.filter((_, i) => i !== index)))
            }
          >
            <X size={16} />
          </button>
        </div>
      ))}
    </div>
  );
}
