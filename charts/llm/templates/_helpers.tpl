{{- define "llm.configureEnv" -}}
{{- $env := list -}}

{{- $grpcAddress := trimAll " \n\t" (default ":50051" .Values.llm.grpcAddress) -}}
{{- if $grpcAddress }}
{{- $env = append $env (dict "name" "GRPC_ADDRESS" "value" $grpcAddress) -}}
{{- end }}

{{- $httpAddress := trimAll " \n\t" (default ":8080" .Values.llm.httpAddress) -}}
{{- if $httpAddress }}
{{- $env = append $env (dict "name" "HTTP_ADDRESS" "value" $httpAddress) -}}
{{- end }}

{{- $dbSecret := trim (default "" .Values.llm.databaseUrl.existingSecret) -}}
{{- $dbVar := dict "name" "DATABASE_URL" -}}
{{- if $dbSecret }}
  {{- $dbKey := default "database-url" .Values.llm.databaseUrl.existingSecretKey -}}
  {{- $_ := set $dbVar "valueFrom" (dict "secretKeyRef" (dict "name" $dbSecret "key" $dbKey)) -}}
{{- else }}
  {{- $dbValue := trimAll " \n\t" (default "" .Values.llm.databaseUrl.value) -}}
  {{- $dbValue = required "llm.databaseUrl.value is required" $dbValue -}}
  {{- $_ := set $dbVar "value" $dbValue -}}
{{- end }}
{{- $env = append $env $dbVar -}}

{{- $userEnv := .Values.env | default (list) -}}
{{- $_ := set .Values "env" (concat $env $userEnv) -}}
{{- end -}}
